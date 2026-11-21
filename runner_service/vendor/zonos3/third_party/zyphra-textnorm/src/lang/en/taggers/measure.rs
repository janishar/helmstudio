//! `en/taggers/measure.py` (deterministic).

use std::sync::LazyLock;

use crate::lang::en::graph_utils::{Outs, plural, row_index};
use crate::lang::en::lazy::{Reading, branch_on, building, lz};
use crate::lang::en::taggers::cardinal::{
    digit_comma_end, graph, graph_with_and_best, hundred_component, single_digits,
};
use crate::lang::en::taggers::decimal::DecR;
use crate::lang::en::taggers::fraction::FracR;
use crate::lang::en::taggers::{decimal, fraction, ordinal, whitelist};
use crate::lang::en::verbalizers::ordinal::suffix;
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, Row, Table, cost, is_alpha, is_digit, is_nemo_space, is_upper, match_str};

crate::tsv!(UNIT, "en/measure/unit.tsv");
crate::tsv!(MATH_OPS, "en/measure/math_operation.tsv");
crate::labels!(ADDRESS_WORDS, "en/address/address_word.tsv");

/// `unit.tsv` keyed by the ASCII-lowercased key (for the `TO_LOWER` capitalized branch).
static UNIT_LOWER: LazyLock<Table> = LazyLock::new(|| {
    Table::from_rows(
        UNIT.rows()
            .iter()
            .enumerate()
            // `cost` carries the row index into UNIT (not a weight)
            .map(|(i, r)| Row { key: r.key.to_ascii_lowercase(), value: String::new(), cost: i as Cost })
            .collect(),
    )
});

/// `get_formats(address_word.tsv)`.
static ADDRESS_WORD: LazyLock<Table> = LazyLock::new(|| {
    let mut v: Vec<(String, String)> = ADDRESS_WORDS.iter().map(|r| (r[0].clone(), r[1].clone())).collect();
    let cap = |s: &str| {
        let mut c = s.chars();
        c.next().map(|f| f.to_uppercase().collect::<String>() + c.as_str()).unwrap_or_default()
    };
    for r in ADDRESS_WORDS.iter() {
        v.push((format!("{}.", r[0]), r[1].clone()));
        v.push((cap(&r[0]), cap(&r[1])));
        v.push((format!("{}.", cap(&r[0])), cap(&r[1])));
    }
    Table::from_pairs(v)
});

fn nb(s: &str) -> String {
    s.replace(' ', "\u{a0}")
}

/// `graph_unit` at `p`: `(end, unit.tsv row)`.
fn graph_unit(text: &[char], p: usize, f: &mut dyn FnMut(usize, &Row)) {
    for (r, e) in UNIT.prefix_matches(text, p) {
        f(e, r);
    }
    if p < text.len() && is_upper(text[p]) {
        // the capitalized branch: walk the lowercased trie keyed by `UNIT_LOWER` over the letters at `p`
        let mut lower = [' '; 32];
        let mut n = 0;
        while p + n < text.len() && n < lower.len() && is_alpha(text[p + n]) {
            lower[n] = text[p + n].to_ascii_lowercase();
            n += 1;
        }
        for (lr, l) in UNIT_LOWER.prefix_matches(&lower[..n], 0) {
            if l < 2 {
                continue;
            }
            let row = &UNIT.rows()[lr.cost as usize];
            let ok = row.key.chars().zip(&text[p..p + l]).enumerate().all(|(k, (kc, &tc))| {
                if k == 0 { kc == tc.to_ascii_lowercase() } else { kc == tc || kc == tc.to_ascii_lowercase() }
            });
            if ok {
                f(p + l, row);
            }
        }
    }
}

/// `p`, and `p + 1` when a whitespace char is there (`delete_zero_or_one_space`).
fn opt_space(text: &[char], p: usize) -> impl Iterator<Item = usize> {
    let second = (p < text.len() && is_nemo_space(text[p])).then_some(p + 1);
    std::iter::once(p).chain(second)
}

/// `graph_unit2`: `"/"→per`, one optional space, NBSP, unit: `(end, cost, unit.tsv row)`.
fn unit2(text: &[char], p: usize, f: &mut dyn FnMut(usize, Cost, u16)) {
    if text.get(p) != Some(&'/') {
        return;
    }
    for q in opt_space(text, p + 1) {
        graph_unit(text, q, &mut |e, r| {
            if crate::lang::en::lazy::keep_end(text, e) {
                f(e, r.cost, row_index(&UNIT, r))
            }
        });
    }
}

/// One `units: "…"` field: the unit row (plural or singular form) and the row after `/` (`per`).
#[derive(Clone, Copy, Debug, Default)]
pub struct UnitR {
    unit: Option<u16>,
    per: Option<u16>,
    plural: bool,
}

/// Per `unit.tsv` row: the value and its plural, spaces as NBSP (the forms `units: "…"` holds).
static UNIT_FORMS: LazyLock<Vec<[String; 2]>> =
    LazyLock::new(|| UNIT.rows().iter().map(|r| [nb(&r.value), nb(&plural(&r.value))]).collect());

/// Appends the `units: "…"` field of `u` to `s`.
fn write_units(s: &mut String, u: UnitR) {
    let forms = &*UNIT_FORMS;
    s.push_str("units: \"");
    if let Some(i) = u.unit {
        s.push_str(&forms[i as usize][u.plural as usize]);
    }
    if let Some(p) = u.per {
        if u.unit.is_some() {
            s.push('\u{a0}');
        }
        s.push_str("per\u{a0}");
        s.push_str(&forms[p as usize][0]);
    }
    s.push('"');
}

/// [`write_units`] into a new string while tags are built, else empty.
fn units_text(u: UnitR) -> String {
    let mut s = String::new();
    if building() {
        write_units(&mut s, u);
    }
    s
}

/// `unit_plural` / `unit_singular` field body (`units: "…"`) at `p`.
/// Only ends a token may end at are produced (every caller ends its measure token at the unit's end).
fn units(text: &[char], p: usize, plural_form: bool, out: &mut Outs<UnitR>) {
    let keep = |e: usize| crate::lang::en::lazy::keep_end(text, e);
    // a "/unit" may follow the unit (after at most one whitespace char)
    let slash_after = |e: usize| opt_space(text, e).any(|q| text.get(q) == Some(&'/'));
    let mut base: Vec<(usize, Cost, u16)> = Vec::new();
    graph_unit(text, p, &mut |e, r| {
        if keep(e) || slash_after(e) {
            base.push((e, r.cost, row_index(&UNIT, r)));
        }
    });
    let u = UnitR { unit: None, per: None, plural: plural_form };
    for &(e, c, row) in &base {
        let u = UnitR { unit: Some(row), ..u };
        if keep(e) {
            out.add_r(e, c, units_text(u), u);
        }
        for q in opt_space(text, e) {
            unit2(text, q, &mut |e2, c2, per| {
                if keep(e2) {
                    let u = UnitR { per: Some(per), ..u };
                    out.add_r(e2, c + c2, units_text(u), u)
                }
            });
        }
    }
    unit2(text, p, &mut |e, c, per| {
        if keep(e) {
            let u = UnitR { per: Some(per), ..u };
            out.add_r(e, c, units_text(u), u)
        }
    });
}

/// Range joiners of `cardinal_graph`, by [`CardR`] index.
const CARD_WORDS: [&str; 3] = [" to ", " by ", " times "];

/// One `cardinal_graph` reading: the digit/comma span `num`, and for a range the second span with its joiner
/// (index into [`CARD_WORDS`]).
#[derive(Clone, Copy, Debug, Default)]
pub struct CardR {
    num: (u32, u32),
    range: Option<(u32, u32, u8)>,
}

fn with_and_name(text: &[char], s: u32, e: u32) -> String {
    let buf: String = text[s as usize..e as usize].iter().collect();
    graph_with_and_best(&buf).map(|x| x.0).unwrap_or_default()
}

/// Appends the words of `r` to `s`.
fn write_card(s: &mut String, text: &[char], r: CardR) {
    s.push_str(&with_and_name(text, r.num.0, r.num.1));
    if let Some((a, e, k)) = r.range {
        s.push_str(CARD_WORDS[k as usize]);
        s.push_str(&with_and_name(text, a, e));
    }
}

/// `cardinal_graph = graph_with_and | get_range(graph_with_and)`: best `(end, words, cost)` per end, only for
/// ends `keep` accepts (numbers are named only when some end survives).
pub fn card_spans(text: &[char], p: usize, keep: &dyn Fn(usize) -> bool) -> Outs<CardR> {
    let mut out = Outs::default();
    if p >= text.len() || !is_digit(text[p]) {
        return out;
    }
    let e = digit_comma_end(text, p);
    let mut tails: Vec<(usize, u8)> = Vec::new();
    for (sep, word) in [("-", 0), (" - ", 0), (" x ", 1), ("x", 1), ("*", 2), (" * ", 2)] {
        let Some(l) = match_str(text, e, sep) else { continue };
        let q = e + l;
        if q < text.len() && is_digit(text[q]) && keep(digit_comma_end(text, q)) {
            tails.push((q, word));
        }
    }
    if !keep(e) && tails.is_empty() {
        return out;
    }
    let Some((_, w, c)) = with_and_spans(text, p).pop() else { return out };
    let num = (p as u32, e as u32);
    if keep(e) {
        out.add_r(e, c, w.clone(), CardR { num, range: None });
    }
    for (q, word) in tails {
        for (e2, w2, c2) in with_and_spans(text, q) {
            let r = CardR { num, range: Some((q as u32, e2 as u32, word)) };
            out.add_r(e2, c + c2, lz!("{w}{}{w2}", CARD_WORDS[word as usize]), r);
        }
    }
    out
}

/// Whether some unit form (`unit_plural`/`unit_singular`) can match at `p` and end where a token may end, or be
/// continued by a `/unit` (checked on the unit graph itself; no strings are built).
fn unit_starts(text: &[char], p: usize) -> bool {
    if p >= text.len() {
        return false;
    }
    if text[p] == '/' {
        return true;
    }
    let mut ok = false;
    graph_unit(text, p, &mut |e, _| {
        ok |= crate::lang::en::lazy::keep_end(text, e) || text.get(e) == Some(&'/') || text.get(e + 1) == Some(&'/');
    });
    ok
}

fn starts_am_fm(text: &[char], p: usize) -> bool {
    match_str(text, p, "AM").is_some() || match_str(text, p, "FM").is_some()
}

/// `graph_with_and` best output for every digit/comma prefix at `p`.
fn with_and_spans(text: &[char], p: usize) -> Vec<(usize, String, Cost)> {
    let mut v = Vec::new();
    if p >= text.len() || !is_digit(text[p]) {
        return v;
    }
    // a cardinal here is followed by a unit, space, separator or operator: never by a digit or a comma, so only
    // the maximal run can be on a path
    let end = digit_comma_end(text, p);
    let mut buf = String::new();
    for e in end..=end {
        buf.clear();
        buf.extend(&text[p..e]);
        if let Some((w, c)) = graph_with_and_best(&buf) {
            v.push((e, w, c));
        }
    }
    v
}

fn ws_end(text: &[char], mut i: usize) -> usize {
    while i < text.len() && is_nemo_space(text[i]) {
        i += 1;
    }
    i
}

/// Address (`get_address_graph`): `(end, value, cost)`.
fn address(text: &[char], start: usize, out: &mut Outs) {
    // necessary: the number is followed by " " and some later word (after a space) starts an address word
    let mut run = start;
    while run < text.len() && is_digit(text[run]) {
        run += 1;
    }
    if text.get(run) != Some(&' ') {
        return;
    }
    // the words between the number and the address word are letters/ordinals/spaces/"."; stop at the first char
    // that cannot occur there (digits are allowed for ordinals)
    let mut lim = run;
    while lim < text.len() && (text[lim].is_ascii_alphanumeric() || text[lim] == ' ' || text[lim] == '.') {
        lim += 1;
    }
    let word_follows =
        (run..lim).filter(|&k| text[k] == ' ').any(|k| ADDRESS_WORD.prefix_matches(text, k + 1).next().is_some());
    if !word_follows {
        return;
    }
    // address_num: priority(d{3,4} split form, cardinal.graph)
    let mut nums: Vec<(usize, String)> = Vec::new();
    let mut run = start;
    while run < text.len() && is_digit(text[run]) {
        run += 1;
    }
    let mut buf = String::new();
    // address_num is followed by " " (direction or words): only the full digit run
    for e in run..=run {
        if e - start > 15 || text.get(e) != Some(&' ') {
            continue;
        }
        buf.clear();
        buf.extend(&text[start..e]);
        let n = e - start;
        let split = if n == 3 || n == 4 {
            let (h, t) = buf.split_at(n - 2);
            let head = hundred_component(h);
            let tail = if let Some(r) = t.strip_prefix('0') {
                hundred_component(r).map(|w| lz!("zero {w}"))
            } else {
                hundred_component(t)
            };
            match (head, tail) {
                (Some(a), Some(b)) => Some(lz!("{a} {b}")),
                _ => None,
            }
        } else {
            None
        };
        if let Some(s) = split.or_else(|| graph(&buf)) {
            nums.push((e, s));
        }
    }
    // every address holds an address word (Street, Ave, ...) later in the text
    if nums.is_empty() {
        return;
    }
    for (ne, nw) in nums {
        // direction
        let mut dirs = vec![(ne, nw.clone())];
        if text.get(ne) == Some(&' ') {
            let d = match text.get(ne + 1) {
                Some('E') => Some("East"),
                Some('S') => Some("South"),
                Some('W') => Some("West"),
                Some('N') => Some("North"),
                _ => None,
            };
            if let Some(d) = d {
                dirs.push((ne + 2, lz!("{nw} {d}")));
                if text.get(ne + 2) == Some(&'.') {
                    dirs.push((ne + 3, lz!("{nw} {d}")));
                }
            }
        }
        for (de, dw) in dirs {
            if text.get(de) != Some(&' ') {
                continue;
            }
            // (ordinal_num? | UPPER ALPHA+) " "
            let p = de + 1;
            let mut firsts: Vec<(usize, String)> = Vec::new();
            if text.get(p) == Some(&' ') {
                firsts.push((p + 1, lz!("{dw}  ")));
            }
            ordinal::ordinal_spans(text, p, &mut |e, w| {
                if text.get(e) == Some(&' ') {
                    firsts.push((e + 1, lz!("{dw} {} ", suffix(&w))));
                }
            });
            if p < text.len() && is_upper(text[p]) {
                let mut e = p + 1;
                while e < text.len() && is_alpha(text[e]) {
                    e += 1;
                    if text.get(e) == Some(&' ') {
                        firsts.push((e + 1, lz!("{dw} {} ", text[p..e].iter().collect::<String>())));
                    }
                }
            }
            for (fe, fw) in firsts {
                // (UPPER ALPHA* " ")* address_word: the capitalized words are copied verbatim, so the walk carries
                // positions and the body is the prefix `fw` + `text[fe..q]` + the address word's value
                let mut frontier = vec![fe];
                while let Some(q) = frontier.pop() {
                    for (r, we) in ADDRESS_WORD.prefix_matches(text, q) {
                        let mut body = String::new();
                        if building() {
                            body.reserve(fw.len() + (q - fe) + r.value.len());
                            body.push_str(&fw);
                            body.extend(&text[fe..q]);
                            body.push_str(&r.value);
                        }
                        address_tail(text, we, &body, out);
                    }
                    if q < text.len() && is_upper(text[q]) {
                        let mut e = q + 1;
                        loop {
                            if text.get(e) == Some(&' ') {
                                frontier.push(e + 1);
                            }
                            if e < text.len() && is_alpha(text[e]) {
                                e += 1;
                            } else {
                                break;
                            }
                        }
                    }
                }
            }
        }
    }
}

fn address_tail(text: &[char], we: usize, body: &str, out: &mut Outs) {
    // only ends where a token may end are used (the address is the whole measure token)
    let keep = |e: usize| crate::lang::en::lazy::keep_end(text, e);
    if keep(we) {
        out.add(we, 0, body.to_string());
    }
    if text.get(we) == Some(&'.') {
        if keep(we + 1) {
            out.add(we + 1, 0, body.to_string());
        }
        // address_eos: "." " "{1,2} UPPER (ALPHA|-)*  (−0.001)
        for sp in [1usize, 2] {
            let p = we + 1 + sp;
            if !(1..=sp).all(|k| text.get(we + k) == Some(&' ')) {
                continue;
            }
            if p < text.len() && is_upper(text[p]) {
                let mut e = p + 1;
                if keep(e) {
                    out.add(e, cost(-0.001), text_join(body, &text[we..e]));
                }
                while e < text.len() && (is_alpha(text[e]) || text[e] == '-') {
                    e += 1;
                    if keep(e) {
                        out.add(e, cost(-0.001), text_join(body, &text[we..e]));
                    }
                }
            }
        }
    }
    // (city + state + zip)?: the city is `we` itself or ", " (ALPHA | " ")+ after it; strings are built for kept
    // ends only
    let city_end = if text.get(we) == Some(&',') && text.get(we + 1) == Some(&' ') {
        let mut e = we + 2;
        while e < text.len() && (is_alpha(text[e]) || text[e] == ' ') {
            e += 1;
        }
        e
    } else {
        we
    };
    let zip_at = |se: usize, skip: usize| -> Option<usize> {
        if skip == 1 && text.get(se) != Some(&',') {
            return None;
        }
        let p = se + skip;
        if text.get(p) != Some(&' ') {
            return None;
        }
        let z = text.get(p + 1..p + 6)?;
        (z.iter().all(|&c| is_digit(c)) && keep(p + 6)).then_some(p)
    };
    // one (state end, state value) per city: the city alone, then ", " + each state key
    let emit = |ce: usize, se: usize, st: Option<&str>, out: &mut Outs| {
        let build = |o: &mut String| {
            if !building() {
                return;
            }
            o.push_str(body);
            o.extend(&text[we..ce]);
            if let Some(st) = st {
                o.push_str(", ");
                o.push_str(st);
            }
        };
        if se != we && keep(se) {
            let mut o = String::new();
            build(&mut o);
            out.add(se, 0, o);
        }
        for (skip, comma) in [(0usize, ""), (1, ",")] {
            let Some(p) = zip_at(se, skip) else { continue };
            let mut o = String::new();
            if building() {
                let z: String = text[p + 1..p + 6].iter().collect();
                build(&mut o);
                o.push_str(comma);
                o.push(' ');
                o.push_str(&single_digits(&z).unwrap());
            }
            out.add(p + 6, 0, o);
        }
    };
    let cities = std::iter::once(we).chain(if city_end > we { we + 3..city_end + 1 } else { 0..0 });
    for ce in cities {
        emit(ce, ce, None, out);
        if text.get(ce) == Some(&',') && text.get(ce + 1) == Some(&' ') {
            for (r, se) in whitelist::STATE_INV.prefix_matches(text, ce + 2) {
                emit(ce, se, Some(r.value.as_str()), out);
            }
        }
    }
}

fn text_join(body: &str, tail: &[char]) -> String {
    if !building() {
        return String::new();
    }
    let mut s = body.to_string();
    s.extend(tail);
    s
}

/// `(cardinal_graph | NEMO_ALPHA)` at `p`.
fn card_or_alpha(text: &[char], p: usize) -> Vec<(usize, String, Cost)> {
    let mut v: Vec<(usize, String, Cost)> =
        card_spans(text, p, &|_| true).into_outputs().map(|(e, c, s)| (e, s, c)).collect();
    if p < text.len() && is_alpha(text[p]) {
        v.push((p + 1, text[p].to_string(), 0));
    }
    v
}

fn delims(text: &[char], p: usize) -> Vec<usize> {
    let mut v = vec![p];
    if text.get(p) == Some(&' ') {
        v.push(p + 1);
    }
    v
}

fn math(text: &[char], start: usize, out: &mut Outs) {
    let ops = |p: usize| -> Vec<(usize, String)> {
        MATH_OPS.prefix_matches(text, p).map(|(r, e)| (e, r.value.clone())).collect()
    };
    let eq = |p: usize| -> Option<usize> { (text.get(p) == Some(&'=')).then_some(p + 1) };
    for (e1, w1, c1) in card_or_alpha(text, start) {
        for d1 in delims(text, e1) {
            // form 1: X op (delim | ALPHA) card delim = delim X
            for (oe, ow) in ops(d1) {
                let mut mids: Vec<(usize, String)> = delims(text, oe).into_iter().map(|q| (q, String::new())).collect();
                if oe < text.len() && is_alpha(text[oe]) {
                    mids.push((oe + 1, text[oe].to_string()));
                }
                for (m, mw) in mids {
                    let sep = if mw.is_empty() { " ".to_string() } else { mw };
                    for (e2, w2, c2) in card_spans(text, m, &|_| true).into_outputs().map(|(e, c, s)| (e, s, c)) {
                        for d2 in delims(text, e2) {
                            let Some(qe) = eq(d2) else { continue };
                            for d3 in delims(text, qe) {
                                for (e3, w3, c3) in card_or_alpha(text, d3) {
                                    out.add(e3, c1 + c2 + c3, lz!("{w1} {ow}{sep}{w2} equals {w3}"));
                                }
                            }
                        }
                    }
                }
            }
            // form 2: X = X op card
            if let Some(qe) = eq(d1) {
                for d2 in delims(text, qe) {
                    for (e2, w2, c2) in card_or_alpha(text, d2) {
                        for d3 in delims(text, e2) {
                            for (oe, ow) in ops(d3) {
                                for d4 in delims(text, oe) {
                                    for (e3, w3, c3) in
                                        card_spans(text, d4, &|_| true).into_outputs().map(|(e, c, s)| (e, s, c))
                                    {
                                        out.add(e3, c1 + c2 + c3, lz!("{w1} equals {w2} {ow} {w3}"));
                                    }
                                }
                            }
                        }
                    }
                }
            }
        }
    }
}

/// The `units: "…"` field after a `subgraph_decimal` number.
#[derive(Clone, Copy, Debug)]
pub enum DecUnits {
    /// A unit (with the sign written before the number).
    Unit(UnitR),
    /// `AM` (`FM` when false).
    Am(bool),
    /// The letters' span after the `-`.
    Alpha(u32, u32),
    /// `x`.
    X,
}

/// One `MeasureFst` reading for the branches whose winners are written without a rerun (see [`tag`]); the sign
/// is `text[start] == '-'`. Address and math winners rerun.
#[derive(Clone, Copy, Debug)]
pub enum MeasureR {
    /// `decimal { … } units: "…"`.
    Dec(DecR, DecUnits),
    /// `cardinal { integer: "…" } units: "…"`; `true`: a lone `1` (named "one").
    Card(CardR, bool, UnitR),
    /// `fraction { … } units: "…"`.
    Frac(FracR, UnitR),
    /// `cardinal { integer: "-" } units: "per …"`: the unit row.
    Per(u16),
    /// `units: "<letters>-" decimal { … }`: the end of the letters.
    AlphaDec(u32, DecR),
}

/// The `measure { … }` tag of reading `r` at `start`.
fn tag(text: &[char], start: usize, r: MeasureR) -> String {
    let negs = if text[start] == '-' { "negative: \"true\" " } else { "" };
    let mut s = String::with_capacity(128);
    s.push_str("measure { ");
    match r {
        MeasureR::Dec(d, u) => {
            s.push_str("decimal { ");
            if let DecUnits::Unit(_) = u {
                s.push_str(negs);
            }
            decimal::write_into(&mut s, text, d);
            s.push_str(" } ");
            match u {
                DecUnits::Unit(u) => write_units(&mut s, u),
                DecUnits::Am(am) => s.push_str(if am { "units: \"AM\"" } else { "units: \"FM\"" }),
                DecUnits::Alpha(a, e) => {
                    s.push_str("units: \"");
                    s.extend(&text[a as usize..e as usize]);
                    s.push('"');
                }
                DecUnits::X => s.push_str("units: \"x\""),
            }
        }
        MeasureR::Card(c, one, u) => {
            s.push_str("cardinal { ");
            s.push_str(negs);
            s.push_str("integer: \"");
            if one {
                s.push_str("one");
            } else {
                write_card(&mut s, text, c);
            }
            s.push_str("\" } ");
            write_units(&mut s, u);
        }
        MeasureR::Frac(f, u) => {
            s.push_str("fraction { ");
            fraction::write_into(&mut s, text, f);
            s.push_str(" } ");
            write_units(&mut s, u);
        }
        MeasureR::Per(row) => {
            s.push_str("cardinal { integer: \"-\" } units: \"per\u{a0}");
            s.push_str(&UNIT_FORMS[row as usize][0]);
            s.push_str("\" preserve_order: true");
        }
        MeasureR::AlphaDec(a, d) => {
            s.push_str("units: \"");
            s.extend(&text[start..=a as usize]);
            s.push_str("\" decimal { ");
            decimal::write_into(&mut s, text, d);
            s.push_str(" } preserve_order: true");
        }
    }
    s.push_str(" }");
    s
}

/// Every `(end, cost, body)` of `MeasureFst` from `start`, with the reading of each (`None`: address, math).
pub fn measure_spans(text: &[char], start: usize, out: &mut Outs<Option<MeasureR>>) {
    let c0 = text[start];
    let neg = c0 == '-';
    let s0 = if neg { start + 1 } else { start };
    let negs = if neg { "negative: \"true\" " } else { "" };
    let numeric = s0 < text.len() && (is_digit(text[s0]) || text[s0] == '.');
    // branches (`lazy::branch_on`): 1 decimal, 2 cardinal, 3 fraction, 4 "/unit", 5 alpha-dash-decimal, 6 address,
    // 7 math
    if numeric && branch_on(1) {
        out.set_branch(1);
        // subgraph_decimal
        let mut dec = Outs::default();
        let unit_next = |e: usize| {
            let ue = ws_end(text, e);
            unit_starts(text, ue) || (!neg && (matches!(text.get(e), Some('-' | 'x' | 'X')) || starts_am_fm(text, ue)))
        };
        decimal::wo_negative(text, s0, false, &unit_next, &mut dec);
        for (&(de, dc, ref ds), d) in dec.entries() {
            let ue = ws_end(text, de);
            let mut u = Outs::default();
            units(text, ue, true, &mut u);
            for ((e, c, us), ur) in u.entries() {
                out.add_r(*e, dc + c, lz!("decimal {{ {negs}{ds} }} {us}"), Some(MeasureR::Dec(d, DecUnits::Unit(ur))));
            }
            if !neg {
                for am in ["AM", "FM"] {
                    if match_str(text, ue, am).is_some() {
                        let r = Some(MeasureR::Dec(d, DecUnits::Am(am == "AM")));
                        out.add_r(ue + 2, dc, lz!("decimal {{ {ds} }} units: \"{am}\""), r);
                    }
                }
                if text.get(de) == Some(&'-') {
                    let mut e = de + 1;
                    while e < text.len() && is_alpha(text[e]) {
                        e += 1;
                        let r = Some(MeasureR::Dec(d, DecUnits::Alpha(de as u32 + 1, e as u32)));
                        let a = lz!("{}", text[de + 1..e].iter().collect::<String>());
                        out.add_r(e, dc, lz!("decimal {{ {ds} }} units: \"{a}\""), r);
                    }
                }
                if matches!(text.get(de), Some('x' | 'X')) {
                    out.add_r(de + 1, dc, lz!("decimal {{ {ds} }} units: \"x\""), Some(MeasureR::Dec(d, DecUnits::X)));
                }
            }
        }
    }
    if numeric && branch_on(2) {
        out.set_branch(2);
        // subgraph_cardinal: only spans followed by a unit can match
        for (&(ce, cc, ref cw), cr) in card_spans(text, s0, &|e| unit_starts(text, ws_end(text, e))).entries() {
            let one = ce == s0 + 1 && text[s0] == '1';
            let ue = ws_end(text, ce);
            let mut u = Outs::default();
            units(text, ue, !one, &mut u);
            let w = if one { "one" } else { cw };
            for ((e, c, us), ur) in u.entries() {
                let r = Some(MeasureR::Card(cr, one, ur));
                out.add_r(*e, cc + c, lz!("cardinal {{ {negs}integer: \"{w}\" }} {us}"), r);
            }
        }
    }
    if !neg && branch_on(3) {
        out.set_branch(3);
        // subgraph_fraction
        let mut fr = Outs::default();
        fraction::fraction_spans(text, start, &mut fr);
        for (&(fe, fc, ref fs), f) in fr.entries() {
            let ue = ws_end(text, fe);
            let mut u = Outs::default();
            units(text, ue, true, &mut u);
            for ((e, c, us), ur) in u.entries() {
                out.add_r(*e, fc + c, lz!("fraction {{ {fs} }} {us}"), Some(MeasureR::Frac(f, ur)));
            }
        }
    }
    if !neg && branch_on(4) {
        out.set_branch(4);
        // unit_graph: ("/"→per del01 | "per" del " ") NBSP unit
        let mut heads = Vec::new();
        if c0 == '/' {
            heads.extend(opt_space(text, start + 1));
        }
        if match_str(text, start, "per ").is_some() {
            heads.push(start + 4);
        }
        for h in heads {
            graph_unit(text, h, &mut |e, r| {
                let s = lz!("cardinal {{ integer: \"-\" }} units: \"per\u{a0}{}\" preserve_order: true", nb(&r.value));
                out.add_r(e, r.cost, s, Some(MeasureR::Per(row_index(&UNIT, r))));
            });
        }
    }
    if !neg && branch_on(5) {
        out.set_branch(5);
        // alpha_dash_decimal
        if is_alpha(c0) {
            let mut a = start;
            while a < text.len() && is_alpha(text[a]) {
                a += 1;
            }
            if text.get(a) == Some(&'-') {
                let al: String = if building() { text[start..=a].iter().collect() } else { String::new() };
                let mut dec = Outs::default();
                decimal::wo_negative(text, a + 1, false, &|e| crate::lang::en::lazy::keep_end(text, e), &mut dec);
                for (&(de, dc, ref ds), d) in dec.entries() {
                    let r = Some(MeasureR::AlphaDec(a as u32, d));
                    out.add_r(de, dc, lz!("units: \"{al}\" decimal {{ {ds} }} preserve_order: true"), r);
                }
            }
        }
    }
    if !neg && branch_on(6) && is_digit(c0) {
        out.set_branch(6);
        {
            let mut a = Outs::new();
            address(text, start, &mut a);
            for (e, c, s) in a.into_outputs() {
                out.add_r(e, c, lz!("units: \"address\" cardinal {{ integer: \"{s}\" }} preserve_order: true"), None);
            }
        }
    }
    if !neg && branch_on(7) {
        out.set_branch(7);
        let math_alpha = is_alpha(c0)
            && text
                .get(start + 1)
                .is_some_and(|&n| n == ' ' || n == '=' || MATH_OPS.prefix_matches(text, start + 1).next().is_some());
        // every math form contains "="; only digits, letters, spaces, commas and operators may precede it
        let has_eq = {
            let mut i = start;
            // operands are cardinals (digits, commas, range separators) or single letters: a run of two letters
            // ends the scan, so the cost stays bounded per position
            let lone_alpha = |i: usize| {
                is_alpha(text[i]) && (i == 0 || !is_alpha(text[i - 1])) && text.get(i + 1).is_none_or(|&n| !is_alpha(n))
            };
            while i < text.len() && (text[i].is_ascii_digit() || lone_alpha(i) || " ,-+*/x:÷×·".contains(text[i])) {
                i += 1;
            }
            text.get(i) == Some(&'=')
        };
        if has_eq && (is_digit(c0) || math_alpha) {
            let mut m = Outs::new();
            math(text, start, &mut m);
            for (e, c, s) in m.into_outputs() {
                out.add_r(e, c, lz!("units: \"math\" cardinal {{ integer: \"{s}\" }} preserve_order: true"), None);
            }
        }
    }
}

pub struct Measure;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 1;

impl Classifier for Measure {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut o = Outs::default();
        measure_spans(text, start, &mut o);
        for ((e, c, s), (b, r)) in o.into_entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let cost = cost(1.1) + c;
            crate::lang::en::lazy::record(MEMBER, start, e, cost, b, r.map_or(Reading::None, Reading::Measure));
            out.push(Candidate { end: e, cost, rank: 0, tag: lz!("measure {{ {s} }}") });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[6, 7], text, start, candidate, |r| match r {
            Reading::Measure(r) => Some(tag(text, start, r)),
            _ => None,
        })
    }
}

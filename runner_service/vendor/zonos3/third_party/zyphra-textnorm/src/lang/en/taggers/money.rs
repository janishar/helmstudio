//! `en/taggers/money.py` (deterministic).

use std::sync::LazyLock;

use crate::lang::en::graph_utils::{Outs, Plural, row_index};
use crate::lang::en::lazy::{Reading, branch_on, lz};
use crate::lang::en::taggers::cardinal::{graph_with_and_best, hundred_component, single_digits};
use crate::lang::en::taggers::decimal::{self, DecR};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_alpha, is_digit};

crate::tsv!(MAJ, "en/money/currency_major.tsv");
crate::tsv!(MIN_SINGULAR, "en/money/currency_minor_singular.tsv");
crate::tsv!(MIN_PLURAL, "en/money/currency_minor_plural.tsv");
crate::tsv!(PER_UNITS, "en/money/per_unit.tsv");

static MAJ_P: LazyLock<Plural> = LazyLock::new(|| Plural::new(&MAJ));

fn nb(s: &str) -> String {
    s.replace(' ', "\u{a0}")
}

/// Cheapest of several `(value, cost)` rows for the same key (first on ties).
fn cheapest<'a>(it: impl Iterator<Item = (&'a str, Cost)>) -> Option<(&'a str, Cost)> {
    let mut b: Option<(&str, Cost)> = None;
    for x in it {
        if b.is_none_or(|y| x.1 < y.1) {
            b = Some(x);
        }
    }
    b
}

/// Integer digits/commas from `p`: every prefix end that ends in a digit (`raw` = `text[p..end]` as a string, built
/// by the caller only where needed).
fn int_ends(text: &[char], p: usize) -> impl Iterator<Item = usize> + '_ {
    let end = crate::lang::en::taggers::cardinal::digit_comma_end(text, p);
    (p + 1..=end).filter(move |&e| is_digit(text[e - 1]))
}

fn raw_at(text: &[char], p: usize, e: usize) -> String {
    text[p..e].iter().collect()
}

/// `two_digits_fractional_part`: digits with ≥1 non-zero, trailing zeros deleted, mapped to 2 digits.
fn two_digits(frac: &str) -> Option<String> {
    let t = frac.trim_end_matches('0');
    let b = t.as_bytes();
    if t.is_empty() || b[b.len() - 1] == b'0' {
        return None;
    }
    match t.len() {
        1 => Some(format!("{t}0")),
        2 => Some(if b[0] == b'0' { t[1..].to_string() } else { t.to_string() }),
        _ => None,
    }
}

/// The fields of a `MoneyFst` reading after the currency symbol.
#[derive(Clone, Copy, Debug)]
pub enum MoneyBody {
    /// `graph_integer_only`: the integer digits/commas.
    Int(u32, u32),
    /// `graph_decimal` with a quantity.
    Dec(DecR),
    /// `decimal_delete_last_zeros`: the integer span (empty when absent) and the significant fractional digits.
    DelZeros((u32, u32), (u32, u32)),
    /// `decimal_graph_with_minor`: the integer span (`None`: minor only) and the fractional digits.
    Minor(Option<(u32, u32)>, (u32, u32)),
}

/// One `MoneyFst` reading: the `currency_major.tsv` row of the symbol (its first row), the body, and the
/// `per_unit.tsv` row of a per unit.
#[derive(Clone, Copy, Debug)]
pub struct MoneyR {
    sym: u16,
    body: MoneyBody,
    per: Option<u16>,
}

/// Cheapest `(value, cost)` of `t`'s rows keyed `sym`.
fn cheapest_of<'a>(t: &'a crate::text::Table, sym: &'a str) -> Option<(&'a str, Cost)> {
    cheapest(t.exact(sym).map(|r| (r.value.as_str(), r.cost)))
}

/// The `MoneyFst` fields of reading `r` (inside `money { … }`), appended to `s`.
fn write_into(s: &mut String, text: &[char], r: MoneyR) {
    let sym = MAJ.rows()[r.sym as usize].key.as_str();
    let pl = cheapest_of(&MAJ_P.plural, sym).map_or("", |x| x.0);
    let sg = cheapest_of(&MAJ, sym).map_or("", |x| x.0);
    let raw = |(a, e): (u32, u32)| raw_at(text, a as usize, e as usize);
    let field = |s: &mut String, k: &str, v: &str| {
        s.push_str(k);
        s.push_str(": \"");
        s.push_str(v);
        s.push('"');
    };
    // `integer_part: "…" currency_maj: "…"` or the reverse order, singular for a lone `1`
    let int_maj = |s: &mut String, int: (u32, u32), maj_first: bool| {
        let raw = raw(int);
        let (w, maj) = if raw == "1" {
            ("one".to_string(), sg)
        } else {
            (graph_with_and_best(&raw).map(|x| x.0).unwrap_or_default(), pl)
        };
        if maj_first {
            field(s, "currency_maj", &nb(maj));
            s.push(' ');
            field(s, "integer_part", &w);
        } else {
            field(s, "integer_part", &w);
            s.push(' ');
            field(s, "currency_maj", &nb(maj));
            s.push(' ');
        }
    };
    match r.body {
        MoneyBody::Int(a, e) => int_maj(s, (a, e), true),
        MoneyBody::Dec(d) => {
            field(s, "currency_maj", &nb(pl));
            s.push(' ');
            decimal::write_into(s, text, d);
        }
        MoneyBody::DelZeros(int, sig) => {
            field(s, "currency_maj", &nb(pl));
            s.push(' ');
            if int.0 < int.1 {
                field(s, "integer_part", &graph_with_and_best(&raw(int)).map(|x| x.0).unwrap_or_default());
                s.push(' ');
            }
            s.push(' ');
            field(s, "fractional_part", &single_digits(&raw(sig)).unwrap_or_default());
        }
        MoneyBody::Minor(int, frac) => {
            if let Some(int) = int {
                int_maj(s, int, false);
            }
            let two = two_digits(&raw(frac)).unwrap_or_default();
            let (w, m) = if two == "1" {
                ("one".to_string(), cheapest_of(&MIN_SINGULAR, sym))
            } else {
                (hundred_component(&two).unwrap_or_default(), cheapest_of(&MIN_PLURAL, sym))
            };
            field(s, "fractional_part", &w);
            s.push_str("  ");
            field(s, "currency_min", &nb(m.map_or("", |x| x.0)));
            s.push_str(" preserve_order: true");
        }
    }
    if let Some(p) = r.per {
        s.push(' ');
        field(s, "morphosyntactic_features", &PER_UNITS.rows()[p as usize].value);
    }
}

/// Every `(end, fields, cost)` of `MoneyFst` (before `per_units` and class wrapper).
fn spans(text: &[char], start: usize, out: &mut Outs<Option<MoneyR>>) {
    // currency at start: delete(symbol) forms
    for (r, se) in MAJ.prefix_matches(text, start) {
        let sym = r.key.as_str();
        let maj_pl = cheapest(MAJ_P.plural.exact(sym).map(|r| (r.value.as_str(), r.cost)));
        let maj_sg = cheapest(MAJ.exact(sym).map(|r| (r.value.as_str(), r.cost)));
        let (Some(pl), Some(sg)) = (maj_pl, maj_sg) else { continue };
        // only process each symbol once (duplicate keys)
        if !std::ptr::eq(r, MAJ.exact(sym).next().unwrap()) {
            continue;
        }
        let reading = |body| Some(MoneyR { sym: row_index(&MAJ, r), body, per: None });
        let span = |a: usize, e: usize| (a as u32, e as u32);
        // branches (`lazy::branch_on`): 1 integer, 2 decimal + quantity, 3 decimal deleting last zeros, 4 integer +
        // minor, 5 minor only
        // graph_integer_only + optional_delete_fractional_zeros
        out.set_branch(1);
        for ie in int_ends(text, se).filter(|_| branch_on(1)) {
            let raw = raw_at(text, se, ie);
            let mut bodies: Vec<(String, Cost)> = Vec::new();
            if raw == "1" {
                bodies.push((lz!("currency_maj: \"{}\" integer_part: \"one\"", nb(sg.0)), sg.1));
            } else if let Some((w, c)) = graph_with_and_best(&raw) {
                bodies.push((lz!("currency_maj: \"{}\" integer_part: \"{w}\"", nb(pl.0)), pl.1 + c));
            }
            let rd = reading(MoneyBody::Int(se as u32, ie as u32));
            for (b, c) in &bodies {
                out.add_r(ie, *c, b.clone(), rd);
                if text.get(ie) == Some(&'.') {
                    let mut z = ie + 1;
                    while text.get(z) == Some(&'0') {
                        z += 1;
                        out.add_r(z, *c, b.clone(), rd);
                    }
                }
            }
        }
        // graph_decimal: maj_plural + (decimal_delete_last_zeros | decimal_with_quantity) @ decimal_final_w_abbr
        let mut dec = Outs::default();
        out.set_branch(2);
        // only spans ending in a letter (decimal_with_quantity) are used here
        if branch_on(2) {
            decimal::wo_negative(text, se, true, &|e| e > se && is_alpha(text[e - 1]), &mut dec);
        }
        for ((de, dc, ds), d) in dec.entries() {
            if text[se..*de].last().is_some_and(|&c| is_alpha(c)) {
                out.add_r(*de, pl.1 + dc, lz!("currency_maj: \"{}\" {ds}", nb(pl.0)), reading(MoneyBody::Dec(d)));
            }
        }
        // decimal_delete_last_zeros: int "." d{2,} [1-9] 0* — fractional ≥3 significant digits
        out.set_branch(3);
        for ie in int_ends(text, se).chain(std::iter::once(se)).filter(|_| branch_on(3)) {
            if text.get(ie) != Some(&'.') {
                continue;
            }
            let raw = raw_at(text, se, ie);
            let mut fe = ie + 1;
            let mut frac = String::new();
            while fe < text.len() && is_digit(text[fe]) {
                frac.push(text[fe]);
                fe += 1;
                let sig = frac.trim_end_matches('0');
                if sig.len() >= 3 {
                    let int_part = if raw.is_empty() {
                        String::new()
                    } else {
                        match graph_with_and_best(&raw) {
                            Some((w, _)) => lz!("integer_part: \"{w}\" "),
                            None => continue,
                        }
                    };
                    let ic = if raw.is_empty() { 0 } else { graph_with_and_best(&raw).unwrap().1 };
                    let fr = single_digits(sig).unwrap();
                    let rd = reading(MoneyBody::DelZeros(span(se, ie), span(ie + 1, ie + 1 + sig.len())));
                    let s = lz!("currency_maj: \"{}\" {int_part} fractional_part: \"{fr}\"", nb(pl.0));
                    out.add_r(fe, pl.1 + ic, s, rd);
                }
            }
        }
        // decimal_graph_with_minor (−0.0001)
        let minor = |fe: usize, frac: &str, out: &mut Outs<Option<MoneyR>>, prefix: &str, pc: Cost, rd| {
            let Some(two) = two_digits(frac) else { return };
            if two == "1" {
                if let Some((m, mc)) = cheapest(MIN_SINGULAR.exact(sym).map(|r| (r.value.as_str(), r.cost))) {
                    out.add_r(
                        fe,
                        pc + mc + cost(-0.0001),
                        lz!("{prefix}fractional_part: \"one\"  currency_min: \"{}\" preserve_order: true", nb(m)),
                        rd,
                    );
                }
            } else if let Some(w) = hundred_component(&two) {
                if let Some((m, mc)) = cheapest(MIN_PLURAL.exact(sym).map(|r| (r.value.as_str(), r.cost))) {
                    out.add_r(
                        fe,
                        pc + mc + cost(-0.0001),
                        lz!("{prefix}fractional_part: \"{w}\"  currency_min: \"{}\" preserve_order: true", nb(m)),
                        rd,
                    );
                }
            }
        };
        let frac_ends = |p: usize| -> Vec<(usize, String)> {
            let mut v = Vec::new();
            let mut s = String::new();
            let mut e = p;
            while e < text.len() && is_digit(text[e]) {
                s.push(text[e]);
                e += 1;
                v.push((e, s.clone()));
            }
            v
        };
        out.set_branch(4);
        for ie in int_ends(text, se).filter(|_| branch_on(4)) {
            if text.get(ie) != Some(&'.') {
                continue;
            }
            let raw = raw_at(text, se, ie);
            // integer_plus_maj: digits not "0"; with commas: first digit non-zero
            if raw.contains(',') && raw.starts_with('0') {
                continue;
            }
            if !raw.contains(',') && raw == "0" {
                continue;
            }
            let mut pre: Vec<(String, Cost)> = Vec::new();
            if raw == "1" {
                pre.push((lz!("integer_part: \"one\" currency_maj: \"{}\" ", nb(sg.0)), sg.1));
            } else if let Some((w, c)) = graph_with_and_best(&raw) {
                pre.push((lz!("integer_part: \"{w}\" currency_maj: \"{}\" ", nb(pl.0)), pl.1 + c));
            }
            for (p, pc) in pre {
                for (fe, frac) in frac_ends(ie + 1) {
                    let rd = reading(MoneyBody::Minor(Some(span(se, ie)), span(ie + 1, fe)));
                    minor(fe, &frac, out, &p, pc, rd);
                }
            }
        }
        out.set_branch(5);
        for zero in [0usize, 1].into_iter().filter(|_| branch_on(5)) {
            if zero == 1 && text.get(se) != Some(&'0') {
                continue;
            }
            if text.get(se + zero) != Some(&'.') {
                continue;
            }
            for (fe, frac) in frac_ends(se + zero + 1) {
                let rd = reading(MoneyBody::Minor(None, span(se + zero + 1, fe)));
                minor(fe, &frac, out, "", 0, rd);
            }
        }
    }
}

/// `MoneyFst` body (fields) for every span, incl. optional per-unit, with the reading of each.
pub fn money_spans(text: &[char], start: usize, out: &mut Outs<Option<MoneyR>>) {
    let mut base = Outs::default();
    spans(text, start, &mut base);
    for ((e, c, s), br) in base.with_branches() {
        let (b, r) = *br;
        out.set_branch(b);
        out.add_r(*e, *c, s.clone(), r);
        for (row, pe) in PER_UNITS.prefix_matches(text, *e) {
            let r = r.map(|r| MoneyR { per: Some(row_index(&PER_UNITS, row)), ..r });
            out.add_r(pe, c + row.cost, lz!("{s} morphosyntactic_features: \"{}\"", row.value), r);
        }
    }
}

pub struct Money;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 3;

fn tag(text: &[char], r: MoneyR) -> String {
    let mut s = String::with_capacity(128);
    s.push_str("money { ");
    write_into(&mut s, text, r);
    s.push_str(" }");
    s
}

impl Classifier for Money {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let mut o = Outs::default();
        money_spans(text, start, &mut o);
        for ((e, c, s), (b, r)) in o.into_entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let cost = cost(1.1) + c;
            crate::lang::en::lazy::record(MEMBER, start, e, cost, b, r.map_or(Reading::None, Reading::Money));
            out.push(Candidate { end: e, cost, rank: 0, tag: lz!("money {{ {s} }}") });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Money(r) => Some(tag(text, r)),
            _ => None,
        })
    }
}

/// `graph_range_money` (`tokenize_and_classify.py`): `closure(money "-"→name "to" money, 1)` at weight 1.1. The
/// tags of consecutive repetitions are concatenated without a separator, exactly as the FST writes them.
pub struct MoneyRange;

impl Classifier for MoneyRange {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // a range needs "-" followed by another currency symbol inside the whitespace-free stretch
        let mut i = start;
        let mut ok = false;
        while i < text.len() && !crate::text::is_nemo_space(text[i]) {
            if text[i] == '-' && MAJ.prefix_matches(text, i + 1).next().is_some() {
                ok = true;
                break;
            }
            i += 1;
        }
        if !ok || MAJ.prefix_matches(text, start).next().is_none() {
            return;
        }
        let mut frontier = vec![(start, 0, String::new())];
        while let Some((p, c0, prefix)) = frontier.pop() {
            let mut first = Outs::default();
            money_spans(text, p, &mut first);
            for (e1, c1, s1) in first.into_outputs() {
                if text.get(e1) != Some(&'-') {
                    continue;
                }
                let mut second = Outs::default();
                money_spans(text, e1 + 1, &mut second);
                for (e2, c2, s2) in second.into_outputs() {
                    let tag = lz!("{prefix}money {{ {s1} }} }} tokens {{ name: \"to\" }} tokens {{ money {{ {s2} }}");
                    let c = c0 + c1 + c2;
                    out.push(Candidate { end: e2, cost: cost(1.1) + c, rank: 0, tag: tag.clone() });
                    if e2 < text.len() && !crate::text::is_nemo_space(text[e2]) {
                        frontier.push((e2, c, tag));
                    }
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize(self, text, start, candidate)
    }
}

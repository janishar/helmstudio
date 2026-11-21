//! `ko/verbalizers/*.py` on one parsed token in one field ordering. Each function consumes the fields in the
//! order its FST deletes them, appends its output to `out`, and returns `None` when that ordering has no path
//! (the caller then truncates `out`). Spaces are emitted exactly as the FSTs insert them (the pipeline collapses
//! runs afterwards).

use std::sync::LazyLock;

use crate::lang::common::{Fields, class};
use crate::lang::ko::graph_utils::data;
use crate::tag::{Dict, Value};
use crate::text::{Table, is_nemo_space};

/// `VerbalizeFst` union on one token body.
pub fn verbalize(token: &Dict) -> Option<String> {
    let mut out = String::new();
    verbalize_into(token, &mut out).then_some(out)
}

/// [`verbalize`] appended to `out`; `false` (and `out` unchanged) when the token has no path.
pub fn verbalize_into(token: &Dict, out: &mut String) -> bool {
    let start = out.len();
    if body(token, out).is_none() {
        out.truncate(start);
        return false;
    }
    // `name`: NBSP → space; other classes: `delete_tokens` maps NBSP → space
    if out[start..].contains('\u{a0}') {
        let s = out[start..].replace('\u{a0}', " ");
        out.truncate(start);
        out.push_str(&s);
    }
    true
}

fn body(token: &Dict, out: &mut String) -> Option<()> {
    let (key, value) = class(token)?;
    match (key, value) {
        // whitelist (`(NEMO_CHAR - " ")+`) ∪ word (exactly one NEMO_NOT_QUOTE char)
        ("name", Value::Str(s)) if !s.contains(' ') || s == " " => out.push_str(s),
        (_, Value::Dict(fields)) => match key {
            "cardinal" => cardinal(fields, out)?,
            "ordinal" => ordinal(fields, out)?,
            "decimal" => decimal(fields, out)?,
            "fraction" => fraction(fields, out)?,
            "date" => date(fields, out)?,
            "time" => time(fields, out)?,
            "money" => money(fields, out)?,
            "telephone" => telephone(fields, out)?,
            "measure" => measure(fields, out)?,
            "electronic" => electronic(fields, out)?,
            _ => return None,
        },
        _ => return None,
    }
    Some(())
}

fn done(f: &Fields) -> Option<()> {
    f.done().then_some(())
}

/// `verbalizers/cardinal.py`.
fn cardinal(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        out.push_str("마이너스 ");
    }
    out.push_str(f.take("integer")?);
    if let Some(c) = f.opt("counter")? {
        out.push_str(c);
    }
    done(&f)
}

/// `verbalizers/ordinal.py`.
fn ordinal(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    out.push_str(f.take("integer")?);
    done(&f)
}

/// `verbalizers/decimal.py`.
fn decimal(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "마이너스" {
            return None;
        }
        out.push_str("마이너스 ");
    }
    out.push_str(nonempty(f.take("integer_part")?)?);
    out.push('점');
    out.push_str(nonempty(f.take("fractional_part")?)?);
    done(&f)
}

fn nonempty(s: &str) -> Option<&str> {
    (!s.is_empty()).then_some(s)
}

/// `denominator_root | denominator` (and the numerator twin): `√…` → `루트 …`; otherwise no `√` at all.
fn root_value(v: &str, out: &mut String) -> Option<()> {
    if let Some(rest) = v.strip_prefix('√') {
        out.push_str("루트 ");
        out.push_str(rest);
    } else if v.contains('√') {
        return None;
    } else {
        out.push_str(v);
    }
    Some(())
}

/// `verbalizers/fraction.py`, including its three particle rewrites.
fn fraction(fields: &Dict, dst: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let mut out = String::new();
    if let Some(n) = f.opt("negative")? {
        out.push_str(n);
        out.push(' ');
    }
    if let Some(i) = f.opt("integer_part")? {
        out.push_str(i);
        out.push(' ');
    }
    root_value(f.take("denominator")?, &mut out)?;
    if f.peek_key() == Some("morphosyntactic_features") {
        // only the plain `분의` value may sit between denominator and numerator
        if f.take("morphosyntactic_features")? != "분의" {
            return None;
        }
    }
    out.push_str("분의 ");
    root_value(f.take("numerator")?, &mut out)?;
    if let Some(m) = f.opt("morphosyntactic_features")? {
        out.push(match m {
            "분의_subject" => '이',
            "분의_topic" => '은',
            "분의_object" => '을',
            _ => return None,
        });
    }
    if !f.done() {
        return None;
    }
    let out = rewrite_pairs(&out, &[("이이", "이가"), ("사이", "사가"), ("오이", "오가"), ("구이", "구가")]);
    let out = rewrite_pairs(&out, &[("이은", "이는"), ("사은", "사는"), ("오은", "오는"), ("구은", "구는")]);
    dst.push_str(&rewrite_pairs(&out, &[("이을", "이를"), ("사을", "사를"), ("오을", "오를"), ("구을", "구를")]));
    Some(())
}

/// Obligatory left-to-right `cdrewrite(string_map(pairs), "", "", Σ*)` with two-char keys.
fn rewrite_pairs(s: &str, pairs: &[(&str, &str)]) -> String {
    let mut out = String::with_capacity(s.len());
    let mut rest = s;
    'outer: while let Some(c) = rest.chars().next() {
        for (from, to) in pairs {
            if let Some(r) = rest.strip_prefix(from) {
                out.push_str(to);
                rest = r;
                continue 'outer;
            }
        }
        out.push(c);
        rest = &rest[c.len_utf8()..];
    }
    out
}

/// `verbalizers/date.py`: the `basic` path (era/year/month/day each optional, then ` weekday`); its
/// `month + " " + weekday` alternative only differs by one space, which the pipeline collapses.
fn date(fields: &Dict, out: &mut String) -> Option<()> {
    // The tagger writes `day: "…"weekday: "…"` (no space) for its full-word weekday branches; the parser then
    // reads one value `…"weekday: "…`, and the verbalizer FST, run on the serialized text, still sees two fields.
    let split;
    let fields = match fields.iter().position(|(_, v)| matches!(v, Value::Str(s) if s.contains("\"weekday: \""))) {
        Some(i) => {
            let Value::Str(s) = &fields[i].1 else { return None };
            let (a, b) = s.split_once("\"weekday: \"")?;
            let mut d = fields.clone();
            d[i].1 = Value::Str(a.to_string().into());
            d.insert(i + 1, ("weekday".into(), Value::Str(b.to_string().into())));
            split = d;
            &split
        }
        None => fields,
    };
    let mut f = Fields::new(fields);
    for key in ["era", "year", "month"] {
        if let Some(v) = f.opt(key)? {
            out.push_str(v);
            out.push(' ');
        }
    }
    if let Some(v) = f.opt("day")? {
        out.push_str(v);
    }
    if let Some(v) = f.opt("weekday")? {
        out.push(' ');
        out.push_str(v);
    }
    done(&f)
}

/// `verbalizers/time.py`: `영분`/`영초` verbalize to nothing.
fn time(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let suffix = f.opt("suffix")?;
    let hours = f.opt("hours")?;
    let minutes = f.opt("minutes")?;
    let seconds = f.opt("seconds")?;
    if !f.done() {
        return None;
    }
    let m = |z| if z == "영분" { "" } else { z };
    let s = |z| if z == "영초" { "" } else { z };
    let parts: [&str; 3] = match (hours, minutes, seconds) {
        (Some(h), Some(mi), Some(se)) => [h, m(mi), s(se)],
        (Some(h), Some(mi), None) => [h, m(mi), ""],
        (Some(h), None, None) => [h, "", ""],
        (None, Some(mi), None) => [m(mi), "", ""],
        (None, None, Some(se)) => [s(se), "", ""],
        _ => return None,
    };
    if let Some(sfx) = suffix {
        out.push_str(sfx);
        out.push(' ');
    }
    let n = if seconds.is_some() && hours.is_some() {
        3
    } else if hours.is_some() && minutes.is_some() {
        2
    } else {
        1
    };
    for (i, p) in parts[..n].iter().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        out.push_str(p);
    }
    Some(())
}

/// `verbalizers/money.py`: 원 (weight 0) prints `<int>원` in either field order; any other currency (0.5)
/// prints `<int> <currency>`; `drop_key_val` keeps the minor value (it only deletes the key and quotes).
fn money(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let (int, cur) = match f.peek_key()? {
        "integer_part" => {
            let i = nonempty(f.take("integer_part")?)?;
            (i, nonempty(f.take("currency_maj")?)?)
        }
        "currency_maj" => {
            let c = f.take("currency_maj")?;
            if c != "원" {
                return None;
            }
            (nonempty(f.take("integer_part")?)?, c)
        }
        _ => return None,
    };
    let minor = f.opt("minor_part")?;
    if !f.done() {
        return None;
    }
    let minor = match minor {
        Some(m) => nonempty(m)?,
        None => "",
    };
    out.push_str(int);
    if cur != "원" {
        out.push(' ');
    }
    out.push_str(cur);
    out.push_str(minor);
    Some(())
}

/// `verbalizers/telephone.py`.
fn telephone(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    if let Some(c) = f.opt("country_code")? {
        out.push_str("국가번호 ");
        out.push_str(nonempty(c)?);
        out.push(' ');
    }
    out.push_str(nonempty(f.take("number_part")?)?);
    if let Some(e) = f.opt("extension")? {
        out.push_str(", 내선 ");
        out.push_str(nonempty(e)?);
    }
    done(&f)
}

/// The nested number of a measure: its class verbalizer, then `마이너스` → `마이너스 `.
fn measure_number(f: &mut Fields) -> Option<String> {
    let key = f.peek_key()?;
    let inner = f.dict(key)?;
    let mut s = String::new();
    match key {
        "decimal" => decimal(inner, &mut s),
        "cardinal" => cardinal(inner, &mut s),
        "fraction" => fraction(inner, &mut s),
        _ => None,
    }?;
    Some(s.replace("마이너스", "마이너스 "))
}

/// `verbalizers/measure.py`: `number units` or `units number preserve_order*`.
fn measure(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let (first, second) = if f.peek_key() == Some("units") {
        let u = nonempty(f.take("units")?)?;
        let n = measure_number(&mut f)?;
        while f.preserve_order() {}
        (u.to_string(), n)
    } else {
        let n = measure_number(&mut f)?;
        (n, nonempty(f.take("units")?)?.to_string())
    };
    done(&f)?;
    out.push_str(&first);
    out.push(' ');
    out.push_str(&second);
    Some(())
}

const DIGIT_KO: [&str; 10] = ["영", "일", "이", "삼", "사", "오", "육", "칠", "팔", "구"];

fn push_digit_rewritten(s: &str, out: &mut String) {
    for c in s.chars() {
        if c.is_ascii_digit() {
            out.push_str(DIGIT_KO[(c as u8 - b'0') as usize]);
        } else {
            out.push(c);
        }
    }
}

static TLD: LazyLock<Table> = LazyLock::new(|| {
    let mut rows = Table::parse(data!("electronic/domain.tsv")).rows().to_vec();
    rows.extend(Table::parse(data!("electronic/extensions.tsv")).rows().iter().cloned());
    Table::from_rows(rows)
});

/// `digit_inline_rewrite @ tld_rewrite @ add_space_before_dot` on a domain value.
fn domain(s: &str, out: &mut String) {
    let mut d = String::with_capacity(s.len() * 2);
    push_digit_rewritten(s, &mut d);
    let c: Vec<char> = d.chars().collect();
    let mut k = 0;
    let first = out.len();
    while k < c.len() {
        match TLD.prefix_matches(&c, k).last() {
            Some((row, end)) => {
                for ch in row.value.chars() {
                    push_dot(out, first, ch);
                }
                k = end;
            }
            None => {
                push_dot(out, first, c[k]);
                k += 1;
            }
        }
    }
}

/// Push with `cdrewrite("닷" → " 닷", NEMO_CHAR, "")`: a space before every 닷 that has a char to its left.
fn push_dot(out: &mut String, first: usize, ch: char) {
    if ch == '닷' && out.len() > first {
        out.push(' ');
    }
    out.push(ch);
}

/// `cdrewrite(delete_extra_space)`: every whitespace run → one space.
fn squeeze(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut in_ws = false;
    for ch in s.chars() {
        if is_nemo_space(ch) {
            if !in_ws {
                out.push(' ');
            }
            in_ws = true;
        } else {
            out.push(ch);
            in_ws = false;
        }
    }
    out
}

/// `verbalizers/electronic.py`: `cc_graph` (a 16-digit domain read in groups of four) before `default_graph`.
fn electronic(fields: &Dict, dst: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let mut out = String::new();
    let protocol = f.opt("protocol")?;
    if let Some(p) = protocol {
        nonempty(p)?;
    }
    let username = f.opt("username")?;
    let dom = nonempty(f.take("domain")?)?;
    if !f.done() {
        return None;
    }
    if let (Some(p), None) = (protocol, username) {
        if p.starts_with("신용카드") && dom.len() == 16 && dom.bytes().all(|b| b.is_ascii_digit()) {
            out.push_str(p);
            out.push(' ');
            for (i, g) in dom.as_bytes().chunks(4).enumerate() {
                if i > 0 {
                    out.push(' ');
                }
                push_digit_rewritten(std::str::from_utf8(g).ok()?, &mut out);
            }
            dst.push_str(&squeeze(&out));
            return Some(());
        }
    }
    if let Some(p) = protocol {
        out.push_str(p);
        out.push(' ');
    }
    if let Some(u) = username {
        push_digit_rewritten(nonempty(u)?, &mut out);
        out.push_str(" 골뱅이 ");
    }
    domain(dom, &mut out);
    dst.push_str(&squeeze(&out));
    Some(())
}

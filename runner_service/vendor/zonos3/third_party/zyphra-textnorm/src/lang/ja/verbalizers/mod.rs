//! `ja/verbalizers/*.py`: `VerbalizeFinalFst` on one token body. The `PostProcessor` is built with every option
//! off (`cdrewrite('', '', '', Σ*)`, the identity), so it is not ported.

use crate::lang::common::{Fields, class};
use crate::tag::{Dict, Value};

/// `VerbalizeFst` on one token body, as an owned string.
pub fn verbalize(token: &Dict) -> Option<String> {
    let mut out = String::new();
    verbalize_into(token, &mut out).then_some(out)
}

/// `VerbalizeFst`: the union of the class verbalizers (`delete_tokens` maps NBSP → space), appended to `out`;
/// `false` (with `out` unchanged) when there is no path.
pub fn verbalize_into(token: &Dict, out: &mut String) -> bool {
    let mark = out.len();
    let Some((name, value)) = class(token) else { return false };
    let ok = match value {
        // word (`name: "<one non-quote char>"`) | whitelist (`name: "<non-space chars>"`): `(NEMO_CHAR - " ")+`
        Value::Str(v) => name == "name" && !v.contains(' ') && push(out, v),
        Value::Dict(fields) => match name {
            "cardinal" => cardinal(fields, out),
            "ordinal" => single(fields, "integer", out),
            "decimal" => decimal(fields, out),
            "fraction" => fraction(fields, out),
            "time" => time(fields, out),
            "date" => date(fields, out),
            _ => None,
        }
        .is_some(),
        _ => false,
    };
    if !ok {
        out.truncate(mark);
        return false;
    }
    if out[mark..].contains('\u{a0}') {
        let mapped = out[mark..].replace('\u{a0}', " ");
        out.truncate(mark);
        out.push_str(&mapped);
    }
    true
}

fn push(out: &mut String, v: &str) -> bool {
    out.push_str(v);
    true
}

/// `cardinal.py`: `[negative] integer`, values verbatim.
fn cardinal(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let neg = f.opt("negative")?;
    let int = f.take("integer")?;
    f.done().then(|| {
        out.push_str(neg.unwrap_or(""));
        out.push_str(int);
    })
}

/// `ordinal.py`.
fn single(fields: &Dict, key: &str, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let v = f.take(key)?;
    f.done().then(|| out.push_str(v))
}

/// `decimal.py`: `[negative] integer_part 点 fractional_part` (both parts non-empty).
fn decimal(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let neg = f.opt("negative")?;
    let int = f.take("integer_part")?;
    let frac = f.take("fractional_part")?;
    f.done().then(|| {
        out.push_str(neg.unwrap_or(""));
        out.push_str(int);
        out.push('点');
        out.push_str(frac);
    })
}

/// `numerator`/`denominator` value: `(√ → ルート)? (NOT_QUOTE − √)*`.
fn root_part(v: &str, out: &mut String) -> Option<()> {
    let rest = match v.strip_prefix('√') {
        Some(r) => {
            out.push_str("ルート");
            r
        }
        None => v,
    };
    if rest.contains('√') {
        return None;
    }
    out.push_str(rest);
    Some(())
}

/// `fraction.py`: `[negative] [integer_part] denominator [morphosyntactic_features] numerator` →
/// `negative integer(荷) denominator 分の numerator`; in the morphosyntactic branch only the key and quotes are
/// deleted (the value, `分の`, is kept) and nothing is inserted.
fn fraction(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    if let Some(neg) = f.opt("negative")? {
        out.push_str(neg);
    }
    if let Some(int) = f.opt("integer_part")? {
        let rest = match int.strip_prefix('√') {
            Some(r) => {
                out.push_str("ルート");
                r
            }
            None => int,
        };
        let body_end = rest.trim_end_matches(['と', '荷']);
        let tail = &rest[body_end.len()..];
        if body_end.contains(['荷', 'と', '√']) || tail.chars().count() > 1 {
            return None;
        }
        out.push_str(body_end);
        out.push_str(if tail.is_empty() { "荷" } else { tail });
    }
    root_part(f.take("denominator")?, out)?;
    match f.opt("morphosyntactic_features")? {
        Some(m) => out.push_str(m),
        None => out.push_str("分の"),
    }
    root_part(f.take("numerator")?, out)?;
    f.done().then_some(())
}

/// `time.py`: `[suffix] (hours minutes seconds | hours minutes | hours | minutes | seconds)` — no
/// `minutes seconds` branch, although the tagger emits one.
fn time(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    let suffix = f.opt("suffix")?;
    let h = f.opt("hours")?;
    let m = f.opt("minutes")?;
    let s = f.opt("seconds")?;
    let ok = matches!(
        (h.is_some(), m.is_some(), s.is_some()),
        (true, true, true) | (true, true, false) | (true, false, false) | (false, true, false) | (false, false, true)
    );
    if !f.done() || !ok {
        return None;
    }
    for v in [suffix, h, m, s].into_iter().flatten() {
        out.push_str(v);
    }
    Some(())
}

/// `date.py`: `[era] [year] [month] [day] [weekday] | month weekday`, values concatenated.
fn date(fields: &Dict, out: &mut String) -> Option<()> {
    let mut f = Fields::new(fields);
    for key in ["era", "year", "month", "day", "weekday"] {
        if let Some(v) = f.opt(key)? {
            out.push_str(v);
        }
    }
    f.done().then_some(())
}

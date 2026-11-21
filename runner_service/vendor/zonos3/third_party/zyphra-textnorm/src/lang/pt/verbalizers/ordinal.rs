//! `pt/verbalizers/ordinal.py`: feminine via `cdrewrite(feminine.tsv, "", " " | [EOS], Σ*)`.

use crate::lang::common::Fields;
use crate::tag::Dict;

crate::tsv!(FEMININE, "pt/ordinals/feminine.tsv");

fn feminine(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    'outer: while i < s.len() {
        for r in FEMININE.rows() {
            if s[i..].starts_with(r.key.as_str()) {
                let after = &s[i + r.key.len()..];
                if after.is_empty() || after.starts_with(' ') {
                    out.push_str(&r.value);
                    i += r.key.len();
                    continue 'outer;
                }
            }
        }
        let c = s[i..].chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    let int = f.take("integer")?;
    let out = match f.take("morphosyntactic_features")? {
        "gender_masc" => int.to_string(),
        "gender_fem" => feminine(int),
        _ => return None,
    };
    f.done().then_some(out)
}

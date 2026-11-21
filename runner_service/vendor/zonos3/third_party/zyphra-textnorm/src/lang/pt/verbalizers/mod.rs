//! `pt/verbalizers/*.py`: each `verbalize` reads one field ordering of a token body and returns `None` where the
//! upstream FST has no path. Spaces doubled by the FSTs are left in (the pipeline collapses them).

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod telephone;
pub mod time;

use crate::lang::common::Fields;
use crate::tag::{Dict, Value};

/// Several pt taggers join fields without a space (`fractional_part: "dois"quantity: "milhões"`), so the parser
/// reads one value `dois"quantity: "milhões`; the serialized permutation reproduces the text and the verbalizer FST
/// reads it as the two fields. Re-split such values: `X"key: "Y` → `(…, X), (key, Y)`.
pub fn expand<'a>(d: &Dict<'a>) -> Dict<'a> {
    let mut out = Dict::with_capacity(d.len());
    for (k, v) in d {
        match v {
            Value::Str(s) if s.contains('"') => {
                let mut key = k.clone();
                let mut rest: &str = s;
                loop {
                    let split = rest.find('"').and_then(|q| {
                        let after = &rest[q + 1..];
                        let kl = after.bytes().take_while(|b| b.is_ascii_alphabetic() || *b == b'_').count();
                        (kl > 0 && after[kl..].starts_with(": \"")).then(|| (q, &after[..kl], &after[kl + 3..]))
                    });
                    match split {
                        Some((q, nk, tail)) => {
                            out.push((key, Value::Str(rest[..q].to_string().into())));
                            key = nk.to_string().into();
                            rest = tail;
                        }
                        None => {
                            out.push((key, Value::Str(rest.to_string().into())));
                            break;
                        }
                    }
                }
            }
            Value::Dict(inner) => out.push((k.clone(), Value::Dict(expand(inner)))),
            other => out.push((k.clone(), other.clone())),
        }
    }
    out
}

/// `closure(cross('negative: "true" ', "menos") + insert_space, 0, 1)`.
pub fn sign(f: &mut Fields, out: &mut String) -> Option<()> {
    if f.peek_key() == Some("negative") {
        if f.take("negative")? != "true" {
            return None;
        }
        out.push_str("menos ");
    }
    Some(())
}

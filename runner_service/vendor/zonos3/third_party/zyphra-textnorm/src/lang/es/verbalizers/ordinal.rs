//! `es/verbalizers/ordinal.py` (deterministic).

use super::s;
use crate::lang::es::graph_utils::shift_number_gender;
use crate::tag::Field;

pub fn verbalize(d: &[Field]) -> Option<String> {
    if d.len() != 2 {
        return None;
    }
    let v = s(d, 0, "integer")?;
    let m = s(d, 1, "morphosyntactic_features")?;
    if v.is_empty() {
        return None;
    }
    match m {
        "gender_masc" => Some(v.to_string()),
        "gender_fem" => {
            // o → a before a space or [EOS]
            let mut e = String::with_capacity(v.len());
            let b = v.as_bytes();
            for (i, c) in v.char_indices() {
                let next = b.get(i + 1);
                if c == 'o' && (next.is_none() || next == Some(&b' ')) {
                    e.push('a');
                } else {
                    e.push(c);
                }
            }
            Some(shift_number_gender(&e))
        }
        "apocope" => {
            Some(v.replace("tercero", "tercer").replace("primero", "primer").replace("undécimo", "decimoprimer"))
        }
        _ => None,
    }
}

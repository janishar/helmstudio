//! `it/verbalizers/money.py`. `optional_add_and` (`"con "`/`"y "` inserted at no weight) is never chosen by the
//! oracle (golden `2,01 ₽` → "due rubli un copeca"), so it is not emitted.

use crate::lang::common::Fields;
use crate::lang::it::taggers::cardinal::{shift_gender, strip_apocope};
use crate::lang::it::taggers::money::{FEM_PLURAL, MASC_PLURAL};
use crate::tag::Dict;

use super::decimal;

#[derive(Clone, Copy, PartialEq)]
enum Gender {
    Masc,
    Fem,
}

/// Genders whose singular or plural projection contains `cur`.
fn genders(cur: &str) -> impl Iterator<Item = Gender> + '_ {
    let has = |t: &crate::text::Table| t.rows().iter().any(|r| r.key == cur || r.value == cur);
    [(Gender::Fem, has(&FEM_PLURAL)), (Gender::Masc, has(&MASC_PLURAL))].into_iter().filter(|x| x.1).map(|x| x.0)
}

fn gendered(n: &str, g: Gender) -> String {
    match g {
        Gender::Fem => shift_gender(n),
        Gender::Masc => n.to_string(),
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let keys: Vec<&str> = fields.iter().map(|(k, _)| k.as_ref()).collect();
    let mut f = Fields::new(fields);
    match keys.first().copied()? {
        // graph_minor
        "fractional_part" => {
            let fr = f.take("fractional_part")?;
            let min = f.take("currency_min")?;
            if !super::end(&mut f) {
                return None;
            }
            let g = genders(min).next()?;
            Some(format!("{} {min}", gendered(fr, g)))
        }
        "integer_part" | "negative" if keys.get(1) == Some(&"currency_maj") && keys[0] == "integer_part" => {
            let int = f.take("integer_part")?;
            let maj = f.take("currency_maj")?;
            if super::end(&mut f) {
                let g = genders(maj).next()?;
                return Some(format!("{} {maj}", gendered(int, g)));
            }
            let fr = f.take("fractional_part")?;
            let min = f.opt("currency_min")?;
            if !super::end(&mut f) {
                return None;
            }
            let mut best: Option<String> = None;
            for g in genders(maj) {
                let head = format!("{} {maj}", gendered(int, g));
                let tail = match min {
                    None => match g {
                        Gender::Fem => shift_gender(fr),
                        Gender::Masc => strip_apocope(fr),
                    },
                    Some(min) => {
                        let mg = genders(min).next()?;
                        format!("{} {min}", gendered(fr, mg))
                    }
                };
                best.get_or_insert(format!("{head} {tail}"));
            }
            best
        }
        _ => {
            // graph_decimal: decimal.graph (with " di" after a quantity) + " " + maj
            let num = decimal::body(&mut f, true)?;
            f.preserve_order();
            let maj = f.take("currency_maj")?;
            genders(maj).next()?;
            f.done().then(|| format!("{num} {maj}"))
        }
    }
}

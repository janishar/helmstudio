//! `de/verbalizers/telephone.py`: `country_code " " number_part`.

use crate::lang::common::Fields;
use crate::tag::Dict;
use crate::text::Cost;

pub fn verbalize(d: &Dict) -> Option<(String, Cost)> {
    let mut f = Fields::new(d);
    let out = format!("{} {}", f.take("country_code")?, f.take("number_part")?);
    while f.preserve_order() {}
    f.done().then_some((out, 0))
}

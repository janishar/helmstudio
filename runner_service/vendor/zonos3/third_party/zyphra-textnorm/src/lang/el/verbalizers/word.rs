use crate::lang::common;
use crate::tag::Dict;

pub(super) fn verbalize(token: &Dict) -> Option<String> {
    let text = common::verbalize_name(token)?;
    (!text.is_empty()).then_some(text)
}

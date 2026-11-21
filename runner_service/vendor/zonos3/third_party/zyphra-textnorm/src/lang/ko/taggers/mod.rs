//! `ko/taggers/*.py`: each `tag(text, pos, …)` yields every accepted `(end, tag body, internal cost)`, streamed
//! to a sink (cardinal, date, time, money, punctuation) or collected (the rare branches); see graph_utils.

pub mod cardinal;
pub mod date;
pub mod decimal;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod punctuation;
pub mod telephone;
pub mod time;
pub mod whitelist;
pub mod word;

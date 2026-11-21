//! `de/verbalizers/*.py`: each `verbalize` takes the fields of one class dict in one ordering and returns the
//! cheapest output with its weight (the weight decides between paths across tokens, see `super::verbalize_split`).

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

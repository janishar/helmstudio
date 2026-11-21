//! `es/taggers/*`.

pub mod cardinal;
pub mod date;
pub mod decimals;
pub mod electronic;
pub mod fraction;
pub mod measure;
pub mod money;
pub mod ordinal;
pub mod telephone;
pub mod time;
pub mod whitelist;
pub mod word;

/// A new text is being tagged: forget the previous text's memoized readings and stashes.
pub fn reset(len: usize) {
    decimals::reset(len);
    date::reset();
    money::reset();
    time::reset();
}

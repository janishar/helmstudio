//! Punctuation definitions for Urdu text normalization.

pub const URDU_PUNCTUATION_MARKS: &str = "!#%&'()*+,-./:;<=>?@^_`{|}~\"،؛؟۔";

pub fn is_single_mark(c: char) -> bool {
    matches!(c, '%' | '٪' | '‰' | '~' | '&' | '§')
}

pub fn is_urdu_punct(c: char) -> bool {
    URDU_PUNCTUATION_MARKS.contains(c) || c.is_ascii_punctuation()
}

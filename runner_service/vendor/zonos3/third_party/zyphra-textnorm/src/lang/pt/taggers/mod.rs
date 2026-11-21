//! `pt/taggers/*.py`.

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
pub mod whitelist;
pub mod word;

use crate::lang::pt::graph_utils::{SpanTagger, is_numeric_word, words_while};
use crate::text::{Cost, is_digit};

/// Length of the word at `start`.
pub fn word_len(text: &[char], start: usize) -> usize {
    text[start..].iter().take_while(|c| !crate::text::is_nemo_space(**c)).count()
}

/// `CardinalFst.fst` (`-`? digits with optional `.`/` ` group separators).
pub struct Cardinal;

impl SpanTagger for Cardinal {
    fn max_words(&self, c: char) -> usize {
        if is_digit(c) || c == '-' { 8 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        // `1 000 000`: a numeric word, then three-digit groups
        if self.max_words(text[start]) == 0 || words_while(text, start, 1, is_numeric_word) == 0 {
            return 0;
        }
        1 + words_while(text, start + word_len(text, start) + 1, 7, |w| w.len() == 3 && w.iter().all(|c| is_digit(*c)))
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        cardinal::tag(span)
    }
}

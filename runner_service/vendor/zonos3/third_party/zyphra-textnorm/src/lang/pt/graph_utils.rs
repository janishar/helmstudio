//! `pt/graph_utils.py`: the span plumbing shared by the pt taggers and `shift_cardinal_gender_pt`.

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, is_nemo_space};

/// A pt tagger that accepts whole spans: `accept` returns the cheapest `(cost, tag)` the classify FST writes for
/// exactly `span` (the classifier's internal weights; the top-level weight is added by [`Spans`]).
pub trait SpanTagger: Send + Sync {
    /// The most whitespace-separated words a span starting with `first` may hold (0: none starts there).
    fn max_words(&self, first: char) -> usize;
    /// The word limit for a span at `text[start..]` (defaults to [`max_words`](Self::max_words)).
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        self.max_words(text[start])
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)>;
}

/// A [`SpanTagger`] with its `add_weight` in `tokenize_and_classify.py`. Only spans that end at a whitespace run or
/// the end of the text are tried: the pt top level puts whitespace between every two tokens.
pub struct Spans<T> {
    pub tagger: T,
    pub weight: Cost,
}

impl<T: SpanTagger> Classifier for Spans<T> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // the pt shape always deletes whitespace runs whole: no token starts on one
        if is_nemo_space(text[start]) {
            return;
        }
        let max = self.tagger.span_limit(text, start);
        let n = text.len();
        let mut end = start;
        let mut words = 0;
        while end < n && words < max {
            while end < n && !is_nemo_space(text[end]) {
                end += 1;
            }
            words += 1;
            if let Some((cost, tag)) = self.tagger.accept(&text[start..end]) {
                out.push(Candidate { end, cost: cost + self.weight, rank: 0, tag });
            }
            while end < n && is_nemo_space(text[end]) {
                end += 1;
            }
        }
    }
}

/// Number of consecutive whitespace-separated words from `start` (at most `max`) that satisfy `ok`.
pub fn words_while(text: &[char], start: usize, max: usize, ok: impl Fn(&[char]) -> bool) -> usize {
    let n = text.len();
    let mut i = start;
    let mut k = 0;
    while i < n && k < max {
        let s = i;
        while i < n && !is_nemo_space(text[i]) {
            i += 1;
        }
        if !ok(&text[s..i]) {
            break;
        }
        k += 1;
        while i < n && is_nemo_space(text[i]) {
            i += 1;
        }
    }
    k
}

/// A word made only of digits and the number punctuation the pt taggers accept (`.`, `,`, `-`, slashes).
pub fn is_numeric_word(w: &[char]) -> bool {
    w.iter().all(|&c| c.is_ascii_digit() || matches!(c, '.' | ',' | '-' | '/' | '\u{2044}' | '\u{2215}'))
}
/// `cdrewrite(cross(from, to), left ∈ lefts (or ""), " " | [EOS] | '"', Σ*)`, left to right.
fn rewrite_word_end(s: &str, from: &str, to: &str, lefts: &[&str]) -> String {
    let mut out = String::with_capacity(s.len() + 4);
    let mut i = 0;
    while i < s.len() {
        let rest = &s[i..];
        if rest.starts_with(from) {
            let after = &rest[from.len()..];
            let right_ok = after.is_empty() || after.starts_with(' ') || after.starts_with('"');
            let left_ok = lefts.is_empty() || lefts.iter().any(|l| s[..i].ends_with(l));
            if right_ok && left_ok {
                out.push_str(to);
                i += from.len();
                continue;
            }
        }
        let c = rest.chars().next().unwrap();
        out.push(c);
        i += c.len_utf8();
    }
    out
}

/// `shift_cardinal_gender_pt`: um→uma, dois→duas, -entos→-entas after the hundreds stems.
pub fn shift_cardinal_gender(s: &str) -> String {
    let s = rewrite_word_end(s, "um", "uma", &[]);
    let s = rewrite_word_end(&s, "dois", "duas", &[]);
    rewrite_word_end(&s, "entos", "entas", &["duz", "trez", "quatroc", "quinh", "seisc", "setec", "oitoc", "novec"])
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn feminine() {
        assert_eq!(shift_cardinal_gender("menos vinte e um"), "menos vinte e uma");
        assert_eq!(shift_cardinal_gender("duzentos e dois"), "duzentas e duas");
        assert_eq!(shift_cardinal_gender("cento e um"), "cento e uma");
    }
}

//! Building blocks shared by every language port: the punctuation classifier (`en/taggers/punctuation.py` and its
//! zh/ja/pt copies), the word verbalizer, field access on a parsed token, and tag-string writers.

use crate::lattice::{Candidate, Classifier};
use crate::tag::{Dict, Field, Value};
use crate::text::{Cost, is_nemo_space, is_unicode_punct};

/// Conservative boundary checks for shapes that separate classified tokens with whitespace or punctuation.
/// Alphanumeric characters cannot be punctuation; all other characters remain possible boundaries.
/// Adjacent-token shapes (`Cjk`, `Ko`, `Thai`) must not use these checks.
#[inline]
pub fn possible_token_start(text: &[char], start: usize) -> bool {
    start == 0 || !text[start - 1].is_alphanumeric()
}

#[inline]
pub fn possible_token_end(text: &[char], end: usize) -> bool {
    end == text.len() || !text[end].is_alphanumeric()
}

/// `PunctuationFst`: `name: "<run>"` for an emphasis tag (`<x>`, `<x/>`, `</x>`; priority union) or a run of marks.
/// Marks: Unicode `P*` except `[`, `]`, plus the file's ASCII string `extra`, plus `[`/`]` when `brackets`, minus
/// the keys of the language's whitelist symbol table.
pub struct Punctuation {
    // Common-script code points use exact source-derived membership bits.
    // Higher code points retain the sorted-table lookup; this is not an alphabet cap.
    low_marks: [u64; 128],
    marks: Vec<char>,
    cost: Cost,
}

crate::labels!(EN_WHITELIST_SYMBOLS, "en/whitelist/symbol.tsv");

impl Punctuation {
    /// `en/taggers/punctuation.py` (also imported by de, es, fr, it): string `s` plus Unicode `P*`, `[`/`]` via
    /// their escaped forms, minus the first column of `en/data/whitelist/symbol.tsv` (`load_labels`).
    pub fn en(cost: Cost) -> Punctuation {
        let symbols: Vec<&'static str> =
            EN_WHITELIST_SYMBOLS.iter().filter_map(|row| row.first()).map(String::as_str).collect();
        Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, symbols, cost)
    }

    pub fn new(
        extra: &str,
        brackets: bool,
        symbols: impl IntoIterator<Item = &'static str>,
        cost: Cost,
    ) -> Punctuation {
        let excluded: Vec<&str> = symbols.into_iter().collect();
        let mut marks: Vec<char> = Vec::new();
        for cp in 0..=0x10FFFFu32 {
            let Some(c) = char::from_u32(cp) else { continue };
            if (is_unicode_punct(c) && c != '[' && c != ']')
                || extra.contains(c)
                || (brackets && (c == '[' || c == ']'))
            {
                let mut buf = [0u8; 4];
                if !excluded.contains(&&*c.encode_utf8(&mut buf)) {
                    marks.push(c);
                }
            }
        }
        marks.sort_unstable();
        marks.dedup();
        let mut low_marks = [0u64; 128];
        marks.retain(|&c| {
            let cp = c as usize;
            if let Some(word) = low_marks.get_mut(cp / 64) {
                *word |= 1u64 << (cp % 64);
                false
            } else {
                true
            }
        });
        Punctuation { low_marks, marks, cost }
    }

    #[inline]
    pub fn is_mark(&self, c: char) -> bool {
        let cp = c as usize;
        match self.low_marks.get(cp / 64) {
            Some(word) => word & (1u64 << (cp % 64)) != 0,
            None => self.marks.binary_search(&c).is_ok(),
        }
    }

    /// `emphasis` accepted exactly on `text[start..end]`.
    fn emphasis_end(text: &[char], start: usize) -> Vec<usize> {
        // "<" + ((X+ "/"?) | ("/" X+)) + ">" with X = NOT_SPACE - {<, >}
        let mut ends = Vec::new();
        if text.get(start) != Some(&'<') {
            return ends;
        }
        let x = |c: char| !is_nemo_space(c) && c != '<' && c != '>';
        let mut j = start + 1;
        while j < text.len() && x(text[j]) {
            j += 1;
        }
        // X+ "/"? ">" : the X run may itself end in "/" (X includes "/"), so any ">" after a non-empty run works
        if j < text.len() && text[j] == '>' && j > start + 1 {
            ends.push(j + 1);
        }
        ends
    }

    /// Every span this classifier accepts at `start`, with its tag.
    pub fn spans(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.spans_with_singletons(text, start, &[], out);
    }

    /// Restrict the supplied marks to one-character spans, independently of the emphasis alternative.
    pub fn spans_with_singletons(&self, text: &[char], start: usize, singletons: &[char], out: &mut Vec<Candidate>) {
        let emph = Self::emphasis_end(text, start);
        let mut end = start;
        while end < text.len() && self.is_mark(text[end]) {
            if singletons.contains(&text[end]) {
                if end == start {
                    out.push(self.candidate(text, start, end + 1));
                }
                break;
            }
            end += 1;
            // priority union: where emphasis accepts the span, its (identical) output replaces punct's
            out.push(self.candidate(text, start, end));
        }
        for e in emph {
            if !out.iter().any(|c| c.end == e) {
                out.push(self.candidate(text, start, e));
            }
        }
    }

    fn candidate(&self, text: &[char], start: usize, end: usize) -> Candidate {
        let mut tag = String::with_capacity(10 + end - start);
        tag.push_str("name: \"");
        tag.extend(&text[start..end]);
        tag.push('"');
        Candidate { end, cost: self.cost, rank: 0, tag }
    }
}

impl Classifier for Punctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.spans(text, start, out);
    }
}

/// The single field of a one-key token body (`{"cardinal": {...}}` → `("cardinal", fields)`).
pub fn class<'t, 'a>(token: &'t Dict<'a>) -> Option<(&'t str, &'t Value<'a>)> {
    match token.as_slice() {
        [(k, v)] => Some((k, v)),
        _ => None,
    }
}

/// A string field value, rejecting the values a `NEMO_NOT_QUOTE`-based verbalizer cannot read.
pub fn str_value<'t>(v: &'t Value<'_>) -> Option<&'t str> {
    match v {
        Value::Str(s) if !s.contains('"') => Some(s),
        _ => None,
    }
}

/// `name: "…"` verbalizer (`en/verbalizers/word.py`, `whitelist.py`): `(NEMO_CHAR - " ")+`, so quotes pass
/// (`name: "a"b"` → `a"b`, checked in the oracle), NBSP → space.
pub fn verbalize_name(fields: &Dict) -> Option<String> {
    let [(k, Value::Str(v))] = fields.as_slice() else { return None };
    if k != "name" || v.contains(' ') {
        return None;
    }
    Some(v.replace('\u{a0}', " "))
}

/// Placeholder grammar of a language whose port has not landed: the tagger has no path (every call raises, so the
/// wrapper keeps the text as typed).
pub struct Unported(pub &'static str);

impl crate::pipeline::Grammar for Unported {
    fn code(&self) -> &'static str {
        self.0
    }
    fn shape(&self) -> crate::lattice::Shape {
        crate::lattice::Shape::En
    }
    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &[]
    }
    fn punct(&self) -> Option<&dyn Classifier> {
        None
    }
    fn verbalize(&self, _token: &Dict) -> Option<String> {
        None
    }
}

/// Ordered field reader for verbalizers that delete `key: "` … `"` in a fixed sequence.
pub struct Fields<'t, 'a> {
    fields: &'t [Field<'a>],
    pos: usize,
}

impl<'t, 'a> Fields<'t, 'a> {
    pub fn new(fields: &'t [Field<'a>]) -> Fields<'t, 'a> {
        Fields { fields, pos: 0 }
    }

    /// The next field if its key is `key` and it is a quote-free string.
    pub fn take(&mut self, key: &str) -> Option<&'t str> {
        let (k, v) = self.fields.get(self.pos)?;
        if k != key {
            return None;
        }
        let s = str_value(v)?;
        self.pos += 1;
        Some(s)
    }

    /// Like [`take`](Self::take) but optional: `Ok(None)` when the next key differs.
    pub fn opt(&mut self, key: &str) -> Option<Option<&'t str>> {
        match self.fields.get(self.pos) {
            Some((k, _)) if k == key => self.take(key).map(Some),
            _ => Some(None),
        }
    }

    /// The next field if it is the nested dict `key`.
    pub fn dict(&mut self, key: &str) -> Option<&'t Dict<'a>> {
        let (k, v) = self.fields.get(self.pos)?;
        match v {
            Value::Dict(d) if k == key => {
                self.pos += 1;
                Some(d)
            }
            _ => None,
        }
    }

    /// `preserve_order: true` when it is next (`delete_preserve_order` / optional deletion).
    pub fn preserve_order(&mut self) -> bool {
        match self.fields.get(self.pos) {
            Some((k, Value::True)) if k == "preserve_order" => {
                self.pos += 1;
                true
            }
            _ => false,
        }
    }

    pub fn peek_key(&self) -> Option<&'t str> {
        self.fields.get(self.pos).map(|(k, _)| k.as_ref())
    }

    pub fn done(&self) -> bool {
        self.pos == self.fields.len()
    }
}

/// Write `key: "value"` (the tagger's field format).
pub fn field(out: &mut String, key: &str, value: &str) {
    out.push_str(key);
    out.push_str(": \"");
    out.push_str(value);
    out.push('"');
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn punctuation_runs_and_emphasis() {
        let p = Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, ["&", "#"], 2_100_000);
        assert!(p.is_mark('['));
        assert!(!p.is_mark('#'));
        let text: Vec<char> = ".,a<b/>".chars().collect();
        let mut out = Vec::new();
        p.candidates(&text, 0, &mut out);
        assert_eq!(out.iter().map(|c| c.end).collect::<Vec<_>>(), [1, 2]);
        out.clear();
        p.candidates(&text, 3, &mut out);
        assert_eq!(out.iter().map(|c| c.end).collect::<Vec<_>>(), [4, 7]);
        assert_eq!(out[1].tag, "name: \"<b/>\"");
    }

    #[test]
    fn singleton_punctuation_preserves_emphasis() {
        let singletons = ['%', '٪', '‰', '~', '&', '§'];
        let p = Punctuation::new("!#%&'()*+,-./:;<=>?@^_`{|}~\"", true, [], 1_100_000);
        for mark in singletons {
            let text: Vec<char> = format!(",{mark}.").chars().collect();
            let mut out = Vec::new();
            p.spans_with_singletons(&text, 0, &singletons, &mut out);
            assert_eq!(out.iter().map(|c| c.end).collect::<Vec<_>>(), [1]);
            out.clear();
            p.spans_with_singletons(&text, 1, &singletons, &mut out);
            assert_eq!(out.iter().map(|c| c.end).collect::<Vec<_>>(), [2]);
        }
        let text: Vec<char> = "<%/>".chars().collect();
        let mut out = Vec::new();
        p.spans_with_singletons(&text, 0, &singletons, &mut out);
        assert_eq!(out.iter().map(|c| c.end).collect::<Vec<_>>(), [1, 4]);
    }
}

//! `pt/taggers/word.py` (deterministic): `_priority_union(convert_space(phoneme), graph)` where
//! `graph = (NOT_SPACE − {$€₩£¥#%} − digits)+ @ −0.0001 | (NOT_SPACE − punct marks)+`.

use std::sync::LazyLock;

use crate::lang::common::Punctuation;
use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, Table, cost, is_alpha, is_digit, is_nemo_space};

crate::tsv!(IPA, "pt/whitelist/ipa_symbols.tsv");

pub struct Word {
    pub punct: &'static Punctuation,
}

static IPA_TABLE: LazyLock<&'static Table> = LazyLock::new(|| &IPA);

fn is_stress(c: char) -> bool {
    matches!(c, 'ˈ' | '\'' | 'ˌ')
}

/// `[` (alpha+ digit* " ")* alpha+ digit* `]`.
fn ascii_phoneme(s: &[char]) -> bool {
    if s.len() < 3 || s[0] != '[' || s[s.len() - 1] != ']' {
        return false;
    }
    s[1..s.len() - 1].split(|&c| c == ' ').all(|unit| {
        let a = unit.iter().take_while(|c| is_alpha(**c)).count();
        a > 0 && unit[a..].iter().all(|c| is_digit(*c))
    })
}

impl Word {
    /// `[` ipa_phonemes `]`: segments `stress? unit+ (stress|unit)*` separated by delimiter runs (punct marks or
    /// spaces), optional trailing delimiters. Simulated as a set of states over char positions.
    fn ipa_phoneme(&self, s: &[char]) -> bool {
        if s.len() < 3 || s[0] != '[' || s[s.len() - 1] != ']' {
            return false;
        }
        let inner = &s[1..s.len() - 1];
        let n = inner.len();
        // states: 0 segment start (stress allowed), 1 after stress (unit needed), 2 in segment, 3 in delimiter
        let mut reach = vec![[false; 4]; n + 1];
        reach[0][0] = true;
        for i in 0..n {
            let st = reach[i];
            if st == [false; 4] {
                continue;
            }
            let c = inner[i];
            let delim = c == ' ' || self.punct.is_mark(c);
            if (st[2] || st[3]) && delim {
                reach[i + 1][3] = true;
            }
            if (st[0] || st[3]) && is_stress(c) {
                reach[i + 1][1] = true;
            }
            if st[2] && is_stress(c) {
                reach[i + 1][2] = true;
            }
            if st[0] || st[1] || st[2] || st[3] {
                for (_, e) in IPA_TABLE.prefix_matches(inner, i) {
                    reach[e][2] = true;
                }
            }
        }
        reach[n][2] || reach[n][3]
    }

    fn excluded(c: char) -> bool {
        matches!(c, '$' | '€' | '₩' | '£' | '¥' | '#' | '%') || is_digit(c)
    }
}

impl SpanTagger for Word {
    fn max_words(&self, c: char) -> usize {
        if c == '[' { 64 } else { 1 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        if text[start] != '[' {
            return 1;
        }
        // a phoneme span ends at the word holding `]`
        let mut k = 0;
        let mut i = start;
        while i < text.len() && k < 64 {
            let s = i;
            while i < text.len() && !is_nemo_space(text[i]) {
                i += 1;
            }
            k += 1;
            if text[s..i].contains(&']') {
                return k;
            }
            while i < text.len() && is_nemo_space(text[i]) {
                i += 1;
            }
        }
        1
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        let name = |nbsp: bool| {
            let mut t = String::with_capacity(span.len() + 10);
            t.push_str("name: \"");
            t.extend(span.iter().map(|&c| if nbsp && c == ' ' { '\u{a0}' } else { c }));
            t.push('"');
            t
        };
        if span[0] == '[' && (ascii_phoneme(span) || self.ipa_phoneme(span)) {
            return Some((0, name(true)));
        }
        if span.iter().any(|&c| is_nemo_space(c)) {
            return None;
        }
        let c = if !span.iter().any(|&c| Self::excluded(c)) {
            cost(-0.0001)
        } else if !span.iter().any(|&c| self.punct.is_mark(c)) {
            0
        } else {
            return None;
        };
        Some((c, name(false)))
    }
}

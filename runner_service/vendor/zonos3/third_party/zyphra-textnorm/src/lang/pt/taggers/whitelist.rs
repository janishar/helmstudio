//! `pt/taggers/whitelist.py` (deterministic, cased, no `input_file`): `data/whitelist/tts.tsv` (empty upstream),
//! the symbol table except `/`, and initialisms `A.B.C` / `A. B. C.` → `ABC`. Values are `convert_space`d.

use std::sync::LazyLock;

use crate::lang::pt::graph_utils::SpanTagger;
use crate::text::{Cost, Table, is_upper, space_to_nbsp};

crate::labels!(TTS, "pt/whitelist/tts.tsv");
crate::labels!(SYMBOLS, "pt/whitelist/symbol.tsv");

static TABLE: LazyLock<Table> = LazyLock::new(|| {
    let rows = TTS.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone()));
    let syms = SYMBOLS.iter().filter(|r| r.len() >= 2 && r[0] != "/").map(|r| (r[0].clone(), r[1].clone()));
    Table::from_pairs(rows.chain(syms))
});

/// Longest key (chars), and the sorted first chars of the keys.
static KEYS: LazyLock<(usize, Vec<char>)> = LazyLock::new(|| {
    let max = TABLE.rows().iter().map(|r| r.key.chars().count()).max().unwrap_or(0);
    let mut first: Vec<char> = TABLE.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    first.sort_unstable();
    first.dedup();
    (max, first)
});

/// `NEMO_UPPER + closure(delete(x) + NEMO_UPPER, 2) + delete(".")?` for `x` in `.`, `. `: the letters.
fn initialism(s: &[char]) -> Option<String> {
    if !s.first().is_some_and(|c| is_upper(*c)) {
        return None;
    }
    for sep in [&['.'][..], &['.', ' '][..]] {
        let mut i = 1;
        let mut n = 0;
        while s[i..].starts_with(sep) && s.get(i + sep.len()).is_some_and(|c| is_upper(*c)) {
            i += sep.len() + 1;
            n += 1;
        }
        if n >= 2 && (i == s.len() || (i + 1 == s.len() && s[i] == '.')) {
            return Some(s.iter().copied().filter(|c| is_upper(*c)).collect());
        }
    }
    None
}

pub struct Whitelist;

impl SpanTagger for Whitelist {
    fn max_words(&self, c: char) -> usize {
        if is_upper(c) || KEYS.1.binary_search(&c).is_ok() { 64 } else { 0 }
    }
    fn span_limit(&self, text: &[char], start: usize) -> usize {
        let c = text[start];
        if self.max_words(c) == 0 {
            return 0;
        }
        // symbol keys are single words; `A. B. C.` initialisms span `X.` words
        let initial = |w: &[char]| matches!(w, [u, '.'] if is_upper(*u)) || matches!(w, [u] if is_upper(*u));
        let multi = if is_upper(c) { crate::lang::pt::graph_utils::words_while(text, start, 64, initial) } else { 0 };
        multi.max(1)
    }
    fn accept(&self, span: &[char]) -> Option<(Cost, String)> {
        if span.len() <= KEYS.0 {
            if let Some((r, _)) = TABLE.prefix_matches(span, 0).find(|&(_, e)| e == span.len()) {
                return Some((r.cost, format!("name: \"{}\"", space_to_nbsp(&r.value))));
            }
        }
        initialism(span).map(|v| (0, format!("name: \"{v}\"")))
    }
}

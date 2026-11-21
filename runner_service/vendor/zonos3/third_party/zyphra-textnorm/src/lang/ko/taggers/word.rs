//! `ko/taggers/word.py`: `name: "<(NEMO_NOT_SPACE - NEMO_DIGIT)+>"` (ja graph_utils classes).
//!
//! A word span `[s, e)` ending inside a word run is only useful when another classifier can start at `e`: the
//! alternative `word [s, e) + word [e, f)` (cost 200) is always beaten by `word [s, f)` (100). Ends where no
//! other classifier can start are therefore skipped; [`can_start`] over-approximates those start chars.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::data;
use crate::text::{Table, csv_rows, is_nemo_space};

/// First chars of every non-word classifier's spans that are Hangul syllables (all other chars are treated as
/// possible starts). The electronic file-name stem may start anywhere in a run holding `.`; callers handle it.
static HANGUL_STARTS: LazyLock<Vec<char>> = LazyLock::new(|| {
    let mut v: Vec<char> = "기반정자마부월화수목금토일공카신체결".chars().collect();
    for src in [data!("whitelist.tsv"), data!("time/division.tsv")] {
        v.extend(Table::parse(src).rows().iter().filter_map(|r| r.key.chars().next()));
    }
    for row in csv_rows(data!("money/currency_major.tsv")) {
        v.extend(row.iter().filter_map(|k| k.chars().next()));
    }
    v.sort_unstable();
    v.dedup();
    v
});

/// Whether a classifier other than word (and electronic's file-name stem) may start at `c`.
pub fn can_start(c: char) -> bool {
    !('가'..='힣').contains(&c) || HANGUL_STARTS.binary_search(&c).is_ok()
}

/// Every useful word span end from `start`.
pub fn ends(text: &[char], start: usize, mut f: impl FnMut(usize)) {
    let word = |c: char| !is_nemo_space(c) && !c.is_ascii_digit();
    let mut run_end = start;
    while run_end < text.len() && word(text[run_end]) {
        run_end += 1;
    }
    // a run holding "." + an ASCII letter or digit is never pruned: the electronic file-name stem (followed by an
    // extension such as `.jpg`) may start at any char
    let prune = !text[start..run_end].windows(2).any(|w| w[0] == '.' && w[1].is_ascii_alphanumeric());
    for end in start + 1..=run_end {
        if prune && end < run_end && !can_start(text[end]) {
            continue;
        }
        f(end);
    }
}

/// The tag of the word `text[start..end]`.
pub fn tag(text: &[char], start: usize, end: usize) -> String {
    let mut tag = String::with_capacity(9 + 3 * (end - start));
    tag.push_str("name: \"");
    tag.extend(&text[start..end]);
    tag.push('"');
    tag
}

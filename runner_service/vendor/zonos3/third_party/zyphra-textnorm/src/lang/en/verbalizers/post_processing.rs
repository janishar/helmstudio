//! `en/verbalizers/post_processing.py` `PostProcessingFst`, applied with `top_rewrite` (shortest path).
//!
//! The cascade: (A) optional deletion of the one space before a mark of `no_space_before_punct` (−0.0001 each);
//! (B) "``" → `"`; (C) delete spaces after `< { ( [`; (D) optional quote pairs `` `…` `` / `"…"` whose content
//! starts with an ASCII letter, inner edge spaces optionally deleted, each quote −0.0001 and the pair −0.0001,
//! a `"` pair followed by an `end` absorbing non-letters (−0.0001 each, space −0.0002); (E) `' ? APOS ' '` → APOS
//! between a letter and `s ` / `s[EOS]`. (A) and (D) interact through the spaces they compete for, so they run as
//! one cost-minimizing DP; (B), (C) and (E) are deterministic rewrites.

use std::sync::LazyLock;

use crate::lang::common::Punctuation;
use crate::text::{Cost, is_alpha};

const APOSTROPHES: [char; 36] = [
    '\'', '´', 'ʹ', 'ʻ', 'ʼ', 'ʽ', 'ʾ', 'ˈ', 'ˊ', 'ˋ', '˴', 'ʹ', '΄', '՚', '՝', 'י', '׳', 'ߴ', 'ߵ', 'ᑊ', 'ᛌ', '᾽', '᾿',
    '`', '´', '῾', '‘', '’', '‛', '′', '‵', 'ꞌ', '＇', '｀', '𖽑', '𖽒',
];

static MARKS: LazyLock<Punctuation> = LazyLock::new(|| Punctuation::en(0));

/// A mark before which a space is deleted (`punct_marks_all` minus the allowed list).
fn no_space_before(c: char) -> bool {
    MARKS.is_mark(c) && !matches!(c, '&' | '\'' | '"' | '«' | '-' | '—' | '<' | '{' | '(' | '[' | '`' | '“')
}

const W: Cost = -100;

#[derive(Clone, Copy, PartialEq, Eq, Debug)]
enum St {
    Out,
    /// After an opening quote (`d` = double), before the first letter; `sp` = the optional space was deleted.
    Open {
        d: bool,
        sp: bool,
    },
    In {
        d: bool,
    },
    /// Deleted the optional space before a closing quote.
    PreClose {
        d: bool,
    },
    End,
}

fn idx(s: St) -> usize {
    match s {
        St::Out => 0,
        St::Open { d, sp } => 1 + d as usize * 2 + sp as usize,
        St::In { d } => 5 + d as usize,
        St::PreClose { d } => 7 + d as usize,
        St::End => 9,
    }
}
const NS: usize = 10;

/// Text without `"`, `` ` `` or an apostrophe variant: only (A) and (C) can apply, and (A)'s deletions are all
/// strictly preferred (each is −0.0001 and none competes for a space), so one pass over the string suffices.
fn apply_plain(text: &str) -> String {
    let mut out = String::with_capacity(text.len());
    let mut it = text.chars().peekable();
    let mut last: Option<char> = None;
    while let Some(c) = it.next() {
        if c == ' ' {
            // (C) after an opening bracket, (A) before a mark that takes no space
            if matches!(last, Some('<' | '{' | '(' | '[')) || it.peek().is_some_and(|&m| no_space_before(m)) {
                continue;
            }
        }
        out.push(c);
        last = Some(c);
    }
    out
}

pub fn apply(text: &str) -> String {
    let quote_like = |c: char| if c.is_ascii() { matches!(c, '"' | '`' | '\'') } else { APOSTROPHES.contains(&c) };
    if !text.chars().any(quote_like) {
        return apply_plain(text);
    }
    // (B) and (C)
    let mut s: Vec<char> = Vec::with_capacity(text.len());
    let src: Vec<char> = text.chars().collect();
    let mut i = 0;
    while i < src.len() {
        if src[i] == '`' && src.get(i + 1) == Some(&'`') {
            s.push('"');
            i += 2;
        } else {
            s.push(src[i]);
            i += 1;
        }
    }
    let mut t: Vec<char> = Vec::with_capacity(s.len());
    for &c in &s {
        if c == ' ' && matches!(t.last(), Some('<' | '{' | '(' | '[')) {
            continue;
        }
        t.push(c);
    }
    let s = t;
    if !s.iter().any(|&c| c == '"' || c == '`') {
        // no quote pairs possible: (A) alone, and its deletions are all strictly preferred
        let mut out: Vec<char> = Vec::with_capacity(s.len());
        for (i, &c) in s.iter().enumerate() {
            if c == ' ' && s.get(i + 1).is_some_and(|&m| no_space_before(m)) {
                continue;
            }
            out.push(c);
        }
        return apostrophes(&out);
    }
    let n = s.len();
    // DP: best[pos][state] = (cost, back (prev state, emitted?))
    #[derive(Clone, Copy)]
    struct Cell {
        cost: Cost,
        prev: u8,
        emit: bool,
    }
    let mut best: Vec<Option<Cell>> = vec![None; (n + 1) * NS];
    let states = |k: usize| -> St {
        match k {
            0 => St::Out,
            1 => St::Open { d: false, sp: false },
            2 => St::Open { d: false, sp: true },
            3 => St::Open { d: true, sp: false },
            4 => St::Open { d: true, sp: true },
            5 => St::In { d: false },
            6 => St::In { d: true },
            7 => St::PreClose { d: false },
            8 => St::PreClose { d: true },
            _ => St::End,
        }
    };
    best[idx(St::Out)] = Some(Cell { cost: 0, prev: 0, emit: false });
    let relax = |best: &mut Vec<Option<Cell>>, pos: usize, to: St, cost: Cost, from: St, emit: bool| {
        let slot = &mut best[pos * NS + idx(to)];
        if slot.is_none_or(|c| cost < c.cost) {
            *slot = Some(Cell { cost, prev: idx(from) as u8, emit });
        }
    };
    for p in 0..=n {
        // epsilon: End -> Out
        if let Some(c) = best[p * NS + idx(St::End)] {
            let slot = &mut best[p * NS + idx(St::Out)];
            if slot.is_none_or(|o| c.cost < o.cost) {
                // mark epsilon with prev = End and emit=false at same position: encode via special flag
                *slot = Some(Cell { cost: c.cost, prev: 100 + idx(St::End) as u8, emit: false });
            }
        }
        if p == n {
            break;
        }
        let c = s[p];
        let del_a = c == ' ' && s.get(p + 1).is_some_and(|&m| no_space_before(m));
        for k in [9, 0, 1, 2, 3, 4, 7, 8, 5, 6] {
            let st = states(k);
            let Some(cell) = best[p * NS + k] else { continue };
            let cur = cell.cost;
            match st {
                St::End => {
                    if !is_alpha(c) {
                        let w = if c == ' ' { 2 * W } else { W };
                        relax(&mut best, p + 1, St::End, cur + w, st, true);
                    }
                    if del_a {
                        relax(&mut best, p + 1, St::End, cur + W, st, false);
                    }
                }
                St::Out => {
                    if del_a {
                        relax(&mut best, p + 1, St::Out, cur + W, st, false);
                    }
                    if c == '"' {
                        relax(&mut best, p + 1, St::Open { d: true, sp: false }, cur + 2 * W, st, true);
                    }
                    if c == '`' {
                        relax(&mut best, p + 1, St::Open { d: false, sp: false }, cur + 2 * W, st, true);
                    }
                    relax(&mut best, p + 1, St::Out, cur, st, true);
                }
                St::Open { d, sp } => {
                    if c == ' ' && !sp {
                        relax(&mut best, p + 1, St::Open { d, sp: true }, cur, st, false);
                    }
                    if is_alpha(c) {
                        relax(&mut best, p + 1, St::In { d }, cur, st, true);
                    }
                }
                St::In { d } => {
                    let q = if d { '"' } else { '`' };
                    if c == ' ' && s.get(p + 1) == Some(&q) {
                        relax(&mut best, p + 1, St::PreClose { d }, cur, st, false);
                    }
                    if c == q {
                        let to = if d { St::End } else { St::Out };
                        relax(&mut best, p + 1, to, cur + W, st, true);
                    }
                    if del_a {
                        relax(&mut best, p + 1, St::In { d }, cur + W, st, false);
                    }
                    relax(&mut best, p + 1, St::In { d }, cur, st, true);
                }
                St::PreClose { d } => {
                    let q = if d { '"' } else { '`' };
                    if c == q {
                        let to = if d { St::End } else { St::Out };
                        relax(&mut best, p + 1, to, cur + W, st, true);
                    }
                }
            }
        }
    }
    // accept in Out (End already folded into Out)
    let mut out: Vec<char> = Vec::with_capacity(n);
    let mut pos = n;
    let mut k = idx(St::Out);
    if best[pos * NS + k].is_none() {
        return apostrophes(&s);
    }
    loop {
        let cell = best[pos * NS + k].expect("dp back-pointer");
        if pos == 0 && k == idx(St::Out) && cell.prev == 0 && !cell.emit {
            break;
        }
        if cell.prev >= 100 {
            k = (cell.prev - 100) as usize;
            continue;
        }
        if cell.emit {
            out.push(s[pos - 1]);
        }
        pos -= 1;
        k = cell.prev as usize;
    }
    out.reverse();
    apostrophes(&out)
}

/// (E) `cdrewrite(" "? APOS " " → APOS, NEMO_ALPHA, "s " | "s[EOS]")`.
fn apostrophes(s: &[char]) -> String {
    let mut out: Vec<char> = Vec::with_capacity(s.len());
    let mut i = 0;
    while i < s.len() {
        if out.last().is_some_and(|&c| is_alpha(c)) {
            let mut j = i;
            if s[j] == ' ' {
                j += 1;
            }
            if j + 1 < s.len() && APOSTROPHES.contains(&s[j]) && s[j + 1] == ' ' {
                let k = j + 2;
                if k < s.len() && s[k] == 's' && (k + 1 == s.len() || s[k + 1] == ' ') {
                    out.push(s[j]);
                    i = k;
                    continue;
                }
            }
        }
        out.push(s[i]);
        i += 1;
    }
    out.into_iter().collect()
}

#[cfg(test)]
mod tests {
    use super::apply;

    #[test]
    fn oracle_cases() {
        for (i, o) in [
            ("a \" hello \" b", "a \"hello\" b"),
            ("\" hello \"", "\"hello\""),
            ("a ` hi ` b", "a `hi` b"),
            ("\" 5 \"", "\" 5 \""),
            ("( a )", "(a)"),
            ("(  a", "(a"),
            ("dog ' s car", "dog's car"),
            ("dog 's", "dog 's"),
            ("a `` b `` c", "a \"b\" c"),
            ("a . b", "a. b"),
            ("a  . b", "a . b"),
            ("\" hi \".", "\"hi\"."),
            ("\" hi \" .", "\"hi\" ."),
            ("a \"b \"c\" d\"", "a \"b\"c\"d\""),
            ("“ hi ”", "“ hi”"),
            ("he said ,\" hi \"", "he said,\"hi\""),
            ("Tom’ s", "Tom’s"),
            ("\" hi\"x\" yo \"", "\"hi\"x\"yo\""),
        ] {
            assert_eq!(apply(i), o, "{i:?}");
        }
    }

    /// Tie: `" hi " , " yo "` — the first pair's `end` absorbing ` , " ` (−0.0008) equals pairing the second
    /// quotes (−0.0005 − 0.0003); the oracle keeps `" yo "` unpaired.
    #[test]
    fn tie_end_absorbs() {
        assert_eq!(apply("\" hi \" , \" yo \""), "\"hi\" , \" yo \"");
    }
}

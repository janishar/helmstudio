//! `sacremoses.MosesDetokenizer(lang).detokenize([text], unescape=False)` (sacremoses 0.1.1, MIT), restricted to the
//! branches reachable for the languages that use it here (en, zh, ja, ko).

use std::sync::LazyLock;

use crate::text::{is_py_space, py_strip};

/// perluniprops `IsAlpha` / `IsSc` (copied from sacremoses 0.1.1, MIT). Sorted for binary search.
static IS_ALPHA: LazyLock<Vec<char>> = LazyLock::new(|| sorted_chars(include_str!("../data/perluniprops/IsAlpha.txt")));
static IS_SC: LazyLock<Vec<char>> = LazyLock::new(|| sorted_chars(include_str!("../data/perluniprops/IsSc.txt")));

fn sorted_chars(s: &str) -> Vec<char> {
    let mut v: Vec<char> = s.chars().collect();
    v.sort_unstable();
    v.dedup();
    v
}

/// `sacremoses.util.is_cjk`: both range ends exclusive, first range with `c < end` decides.
pub fn is_cjk(c: char) -> bool {
    const RANGES: [(u32, u32); 11] = [
        (4352, 4607),
        (11904, 42191),
        (43072, 43135),
        (44032, 55215),
        (63744, 64255),
        (65072, 65103),
        (65381, 65500),
        (94208, 101119),
        (110592, 110895),
        (110960, 111359),
        (131072, 196607),
    ];
    let cp = c as u32;
    for (start, end) in RANGES {
        if cp < end {
            return cp > start;
        }
    }
    false
}

fn is_currency_token(tok: &str) -> bool {
    tok.chars().all(|c| matches!(c, '(' | '[' | '{' | '¿' | '¡') || IS_SC.binary_search(&c).is_ok())
}

fn is_punct_token(tok: &str) -> bool {
    tok.chars().all(|c| matches!(c, ',' | '.' | '?' | '!' | ':' | ';' | '\\' | '%' | '}' | ']' | ')'))
}

fn is_english_contraction(tok: &str) -> bool {
    let mut it = tok.chars();
    it.next() == Some('\'') && it.next().is_some_and(|c| IS_ALPHA.binary_search(&c).is_ok())
}

fn is_open_quote_token(tok: &str) -> bool {
    tok.chars().all(|c| matches!(c, '\'' | '"' | '„' | '“' | '`'))
}

/// `MosesDetokenizer(lang=lang).detokenize([text], unescape=False)`.
pub fn detokenize(lang: &str, text: &str) -> String {
    // `" " + text + " "` with `" @-@ "` → `"-"`, then `split()`
    let replaced;
    let text = if text.contains("@-@") {
        replaced = format!(" {text} ").replace(" @-@ ", "-");
        replaced.as_str()
    } else {
        text
    };
    let tokens = text.split(is_py_space).filter(|p| !p.is_empty());

    // quote_counts: the five predefined keys plus any quote run seen (dict.get default 0). Keys are quote runs of the
    // tokens themselves, so they borrow from `text`.
    let mut quote_counts: Vec<(&str, u64)> = vec![("'", 0), ("\"", 0), ("``", 0), ("`", 0), ("''", 0)];
    let mut prepend = " ";
    let mut out = String::with_capacity(text.len() + 2);
    let mut prev: Option<&str> = None;
    let mut i = 0usize;
    for tok in tokens {
        let first = tok.chars().next().expect("split() yields non-empty tokens");
        if is_cjk(first) && lang != "ko" {
            if prev.is_some_and(|p| p.chars().next_back().is_some_and(is_cjk)) {
                out.push_str(tok);
            } else {
                out.push_str(prepend);
                out.push_str(tok);
            }
            prepend = " ";
        } else if is_currency_token(tok) {
            out.push_str(prepend);
            out.push_str(tok);
            prepend = "";
        } else if is_punct_token(tok) {
            out.push_str(tok);
            prepend = " ";
        } else if lang == "en" && i > 0 && is_english_contraction(tok) {
            out.push_str(tok);
            prepend = " ";
        } else if is_open_quote_token(tok) {
            let normalized = if tok.chars().all(|c| matches!(c, '„' | '“' | '”')) { "\"" } else { tok };
            let slot = match quote_counts.iter().position(|(k, _)| *k == normalized) {
                Some(p) => p,
                None => {
                    quote_counts.push((normalized, 0));
                    quote_counts.len() - 1
                }
            };
            if quote_counts[slot].1 % 2 == 0 {
                if lang == "en" && tok == "'" && prev.is_some_and(|p| p.ends_with('s')) {
                    out.push_str(tok);
                    prepend = " ";
                } else {
                    out.push_str(prepend);
                    out.push_str(tok);
                    prepend = "";
                    quote_counts[slot].1 += 1;
                }
            } else {
                out.push_str(tok);
                prepend = " ";
                quote_counts[slot].1 += 1;
            }
        } else {
            out.push_str(prepend);
            out.push_str(tok);
            prepend = " ";
        }
        prev = Some(tok);
        i += 1;
    }
    // `re.sub("  +", " ", …).strip()`: tokens hold no spaces, so only separators can double up
    let mut collapsed = String::with_capacity(out.len());
    let mut space = false;
    for c in out.chars() {
        if c == ' ' {
            if !space {
                collapsed.push(' ');
            }
            space = true;
        } else {
            collapsed.push(c);
            space = false;
        }
    }
    let stripped = py_strip(&collapsed);
    if stripped.len() == collapsed.len() { collapsed } else { stripped.to_string() }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn cjk_ranges_are_exclusive() {
        assert!(!is_cjk('\u{1100}')); // 4352 itself
        assert!(is_cjk('\u{1101}'));
        assert!(is_cjk('一'));
        assert!(!is_cjk('\u{FE5F}'));
        assert!(is_cjk('\u{33FE}'));
        assert!(!is_cjk('가')); // U+AC00 = 44032, the exclusive start of the Hangul range
        assert!(is_cjk('각'));
    }

    #[test]
    fn english() {
        assert_eq!(detokenize("en", "Hello , world !"), "Hello, world!");
        assert_eq!(detokenize("en", "it ' s"), "it 's");
        assert_eq!(detokenize("en", "it 's fine"), "it's fine");
        assert_eq!(detokenize("en", "the boys ' toys"), "the boys' toys");
        assert_eq!(detokenize("en", "he said \" hi \" ok"), "he said \"hi\" ok");
        assert_eq!(detokenize("en", "costs $ 5 ( approx )"), "costs $5 (approx)");
        assert_eq!(detokenize("en", "a @-@ b"), "a-b");
    }

    #[test]
    fn cjk_and_korean() {
        assert_eq!(detokenize("zh", "我 有 3 个"), "我有 3 个");
        assert_eq!(detokenize("ja", "東京 大阪"), "東京大阪");
        assert_eq!(detokenize("ko", "서울 부산"), "서울 부산");
    }
}

//! `es/taggers/electronic.py`.

use crate::lang::es::{Lazy, Sink};
use crate::text::{cost, is_alpha, is_digit, starts_with};

crate::labels!(SYMBOLS, "es/electronic/symbols.tsv");

/// The first column of `symbols.tsv`; every key is one char (checked at load).
static SYMBOL_CHARS: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    SYMBOLS
        .iter()
        .filter_map(|r| r.first())
        .map(|k| {
            let mut it = k.chars();
            let c = it.next().expect("empty symbol");
            assert!(it.next().is_none(), "multi-char electronic symbol {k:?}");
            c
        })
        .collect()
});

/// Length of one `accepted_characters` unit at `p` (alnum or a non-"." symbol); `all` admits ".".
fn unit(text: &[char], p: usize, all: bool) -> Option<usize> {
    let c = *text.get(p)?;
    (is_alpha(c) || is_digit(c) || ((all || c != '.') && SYMBOL_CHARS.contains(&c))).then_some(1)
}

/// Ends of `chars+` runs from `p`.
fn run(text: &[char], p: usize, all: bool) -> Vec<usize> {
    let mut v = Vec::new();
    let mut e = p;
    while let Some(n) = unit(text, e, all) {
        e += n;
        v.push(e);
    }
    v
}

/// Ends of `accepted+ ("." accepted+)+` from `p` that a token may end at (`ok_end`).
///
/// `accepted` excludes ".", so a match is a run of accepted chars split by single dots: every end inside the
/// second or a later segment is accepted, so the ends are all positions after the first dot that follows a
/// non-empty first segment, as long as no segment is empty.
fn domain(text: &[char], p: usize) -> Vec<usize> {
    let mut out = Vec::new();
    let mut e = p;
    let mut seg_len = 0;
    let mut dots = 0;
    while e < text.len() {
        if text[e] == '.' {
            if seg_len == 0 {
                break;
            }
            dots += 1;
            seg_len = 0;
            e += 1;
            continue;
        }
        let Some(n) = unit(text, e, false) else { break };
        e += n;
        seg_len += 1;
        if dots > 0 && crate::lang::es::ok_end(text, e) {
            out.push(e);
        }
    }
    out
}

fn s(text: &[char], a: usize, b: usize) -> String {
    text[a..b].iter().collect()
}

pub struct Electronic;

impl Lazy for Electronic {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        // every branch contains "." or "@" before the next whitespace
        let mut e = start;
        while e < text.len() && !crate::text::is_nemo_space(text[e]) && text[e] != '.' && text[e] != '@' {
            e += 1;
        }
        if e == text.len() || crate::text::is_nemo_space(text[e]) {
            return;
        }
        let mut push = |end: usize, body: &dyn Fn() -> String, rank: i32| {
            out.push(end, cost(1.11), rank, || format!("electronic {{ {} preserve_order: true }}", body()));
        };
        // domain alone
        for e in domain(text, start) {
            push(e, &|| format!("domain: \"{}\"", s(text, start, e)), 1);
        }
        // url: protocol + domain
        let mut protos = Vec::new();
        for p in ["https://", "http://"] {
            if starts_with(text, start, p) {
                let e = start + p.len();
                protos.push(e);
                if starts_with(text, e, "www.") {
                    protos.push(e + 4);
                }
            }
        }
        if starts_with(text, start, "www.") {
            protos.push(start + 4);
        }
        // longest protocol first: equal-cost ties go to the first candidate ("http://www." + "con.in")
        for pe in protos.into_iter().rev() {
            for e in domain(text, pe) {
                push(e, &|| format!("protocol: \"{}\" domain: \"{}\"", s(text, start, pe), s(text, pe, e)), 0);
            }
        }
        // email
        for ue in run(text, start, true) {
            if text.get(ue) == Some(&'@') {
                for e in domain(text, ue + 1) {
                    push(e, &|| format!("username: \"{}\" domain: \"{}\"", s(text, start, ue), s(text, ue + 1, e)), 0);
                }
            }
        }
        // tag: "@" + (accepted | accepted domain)
        if text.get(start) == Some(&'@') {
            let mut ends = run(text, start + 1, false);
            ends.extend(domain(text, start + 1));
            ends.sort_unstable();
            ends.dedup();
            for e in ends {
                push(e, &|| format!("username: \"{}\"", s(text, start + 1, e)), 0);
            }
        }
    }
}

#[cfg(test)]
mod tests {
    use crate::lang::es::test_util::tags;

    #[test]
    fn protocol_split_beats_bare_domain() {
        // alternatives: protocol "https://www." + domain (rank 0, longest protocol first) vs protocol "https://"
        // + domain "www.nvidia.com" (rank 0, later) vs domain "https://www.nvidia.com" (rank 1)
        assert_eq!(
            tags("https://www.nvidia.com"),
            "tokens { electronic { protocol: \"https://www.\" domain: \"nvidia.com\" preserve_order: true } }"
        );
    }
}

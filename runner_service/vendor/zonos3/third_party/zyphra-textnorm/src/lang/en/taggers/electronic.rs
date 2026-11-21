//! `en/taggers/electronic.py` (deterministic).

use crate::lang::en::graph_utils::{Outs, row_index};
use crate::lang::en::lazy::{Reading, branch_on, building, lz};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, cost, is_alpha, is_digit, is_lower, is_nemo_space, match_str};

crate::tsv!(CC_CUES, "en/electronic/cc_cues.tsv");
crate::tsv!(SYMBOL, "en/electronic/symbol.tsv");
crate::tsv!(DOMAIN, "en/electronic/domain.tsv");
crate::tsv!(WORDS, "en/electronic/words.tsv");

const NEG: Cost = -100;

/// One item of an [`items_dp`] path: a dict word (`words.tsv` row), a char as written, a `symbol.tsv` key or a
/// `domain.tsv` key (rows).
#[derive(Clone, Copy)]
enum Piece {
    Word(u16),
    Char(char),
    Sym(u16),
    Dom(u16),
}

/// The step reaching a DP cell: the previous cell `(position, state)` and the item.
type Cell = Option<(Cost, u32, u8, Piece)>;

/// [`items_dp`] cells: per offset from the start and state (1: the last item is a dict word), the cheapest step.
struct Dp(Vec<[Cell; 2]>);

impl Dp {
    /// The cheapest `(cost, state)` at offset `i` (the dict-word state on ties).
    fn best(&self, i: usize) -> Option<(Cost, u8)> {
        match self.0[i] {
            [Some(a), Some(b)] => Some(if b.0 <= a.0 { (b.0, 1) } else { (a.0, 0) }),
            [Some(a), None] => Some((a.0, 0)),
            [None, Some(b)] => Some((b.0, 1)),
            [None, None] => None,
        }
    }

    /// The output of the path ending at offset `i` in state `st` (consecutive dict words joined by a space).
    fn text(&self, mut i: usize, mut st: u8) -> String {
        let mut steps = Vec::new();
        while i > 0 {
            let (_, pi, pst, piece) = self.0[i][st as usize].expect("path cell");
            steps.push((piece, pst));
            (i, st) = (pi as usize, pst);
        }
        let mut s = String::new();
        for &(piece, pst) in steps.iter().rev() {
            match piece {
                Piece::Word(r) => {
                    if pst == 1 {
                        s.push(' ');
                    }
                    s.push_str(&WORDS.rows()[r as usize].value);
                }
                Piece::Char(c) => s.push(c),
                Piece::Sym(r) => s.push_str(&SYMBOL.rows()[r as usize].key),
                Piece::Dom(r) => s.push_str(&DOMAIN.rows()[r as usize].key),
            }
        }
        s
    }
}

/// Forward DP over the items of `domain_graph` (`domain = true`) or the `username` closure. For every end
/// reachable from `start`, the cheapest path (first found on ties). Items: dict words (consecutive ones joined
/// by a space at an extra −0.0001), single letters (lowercase → uppercase in a domain), digits, symbol keys, and in
/// a domain the common domains (−0.0001).
fn items_dp(text: &[char], start: usize, domain: bool) -> Dp {
    let mut end = start;
    while end < text.len() && !is_nemo_space(text[end]) {
        end += 1;
    }
    let n = end - start;
    let mut best: Vec<[Cell; 2]> = vec![[None, None]; n + 1];
    best[0][0] = Some((0, 0, 0, Piece::Char(' ')));
    let relax = |best: &mut Vec<[Cell; 2]>, j: usize, st: usize, c: Cost, from: (usize, usize), piece: Piece| {
        let slot = &mut best[j][st];
        if slot.is_none_or(|(oc, ..)| c < oc) {
            *slot = Some((c, from.0 as u32, from.1 as u8, piece));
        }
    };
    for i in 0..n {
        for st in 0..2 {
            let Some((c, ..)) = best[i][st] else { continue };
            let p = start + i;
            let ch = text[p];
            for (r, e) in WORDS.prefix_matches(text, p) {
                if e > end {
                    continue;
                }
                let w = if st == 1 { 2 * NEG } else { NEG };
                relax(&mut best, e - start, 1, c + w, (i, st), Piece::Word(row_index(&WORDS, r)));
            }
            let first = i == 0;
            if is_alpha(ch) {
                let o = if domain && is_lower(ch) { ch.to_ascii_uppercase() } else { ch };
                relax(&mut best, i + 1, 0, c, (i, st), Piece::Char(o));
            }
            if is_digit(ch) && !(first && !domain) {
                relax(&mut best, i + 1, 0, c, (i, st), Piece::Char(ch));
            }
            if !(first && !domain) {
                for (r, e) in SYMBOL.prefix_matches(text, p) {
                    if e <= end {
                        relax(&mut best, e - start, 0, c, (i, st), Piece::Sym(row_index(&SYMBOL, r)));
                    }
                }
            }
            if domain {
                for (r, e) in DOMAIN.prefix_matches(text, p) {
                    if e <= end {
                        relax(&mut best, e - start, 0, c + NEG, (i, st), Piece::Dom(row_index(&DOMAIN, r)));
                    }
                }
            }
        }
    }
    Dp(best)
}

/// `domain_graph_with_class_tags` from `p`: `(end, cost, value)`; input `ALPHA NOT_SPACE* (ALPHA|DIGIT|"/")`. The
/// value is spelled only while tags are built and for ends `keep` accepts.
fn class_domain(text: &[char], p: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, Cost, String)) {
    if p >= text.len() || !is_alpha(text[p]) {
        return;
    }
    let dp = items_dp(text, p, true);
    for i in 2..dp.0.len() {
        let Some((c, st)) = dp.best(i) else { continue };
        let last = text[p + i - 1];
        if is_alpha(last) || is_digit(last) || last == '/' {
            let s = if building() && keep(p + i) { dp.text(i, st) } else { String::new() };
            f(p + i, c, s);
        }
    }
}

/// `protocol` forms at `p`: `(end, value, cost)`.
fn protocols(text: &[char], p: usize) -> Vec<(usize, String, Cost)> {
    let mut v = Vec::new();
    let mut starts = Vec::new();
    for (k, o) in [("https", "HTTPS "), ("http", "HTTP ")] {
        if let Some(l) = match_str(text, p, k) {
            if match_str(text, p + l, "://").is_some() {
                starts.push((p + l + 3, lz!("{o}colon slash slash ")));
            }
        }
    }
    if match_str(text, p, "file:///").is_some() {
        v.push((p + 8, "file colon slash slash slash ".to_string(), 0));
    }
    let www = |q: usize| match_str(text, q, "www.").map(|_| q + 4);
    for (e, s) in &starts {
        v.push((*e, s.clone(), 0));
        if let Some(e2) = www(*e) {
            v.push((e2, lz!("{s}WWW dot "), cost(-1000.0)));
        }
    }
    if let Some(e) = www(p) {
        v.push((e, "WWW dot ".to_string(), cost(-1000.0)));
    }
    v
}

/// One-char keys of `electronic/symbol.tsv`, sorted (all its keys are single chars).
static SYMBOL_CHARS: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut v: Vec<char> = SYMBOL.rows().iter().filter_map(|r| r.key.chars().next()).collect();
    v.sort_unstable();
    v
});

/// One `ElectronicFst` reading at a start: the end of each part, and the table row or protocol it uses; the
/// username/domain texts are the [`items_dp`] paths to those ends.
#[derive(Clone, Copy, Debug)]
pub enum ElR {
    /// `username: "…" domain: "…"`: the `@` position.
    Email(u32),
    /// `domain: "<text[start..end]>"` (dotted domains and slash words).
    Span,
    /// `protocol: "…" domain: "…"`: the index into [`protocols`] at the start.
    Proto(u8),
    /// `protocol: "<cc_cues row>" domain: "<digits>"`: the row and the digits' start.
    Cc(u16, u32),
}

/// The text of the [`items_dp`] path from `p` to `e` (`class_domain` form when `domain`).
fn dp_text(text: &[char], p: usize, e: usize, domain: bool) -> String {
    let dp = items_dp(text, p, domain);
    dp.best(e - p).map(|(_, st)| dp.text(e - p, st)).unwrap_or_default()
}

/// The fields of reading `r` for the span `start..end`.
fn fields(text: &[char], start: usize, end: usize, r: ElR) -> String {
    let span = |a: usize, b: usize| text[a..b].iter().collect::<String>();
    match r {
        ElR::Email(at) => {
            let at = at as usize;
            let us = dp_text(text, start, at, false);
            format!("username: \"{us}\" domain: \"{}\"", dp_text(text, at + 1, end, true))
        }
        ElR::Span => format!("domain: \"{}\"", span(start, end)),
        ElR::Proto(k) => {
            let (pe, ps, _) = protocols(text, start).swap_remove(k as usize);
            format!("protocol: \"{ps}\" domain: \"{}\"", dp_text(text, pe, end, true))
        }
        ElR::Cc(row, d) => {
            format!("protocol: \"{}\" domain: \"{}\"", CC_CUES.rows()[row as usize].value, span(d as usize, end))
        }
    }
}

pub fn electronic_spans(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, out: &mut Outs<ElR>) {
    // email: username "@" domain, with a "." somewhere after the "@"
    // branches (`lazy::branch_on`): 1 email, 2 domain, 3 protocol + domain, 4 slash words, 5 cc phrases
    let has_at = text[start..].iter().take_while(|&&c| !is_nemo_space(c)).any(|&c| c == '@');
    out.set_branch(1);
    if is_alpha(text[start]) && has_at && branch_on(1) {
        let ud = items_dp(text, start, false);
        for i in 1..ud.0.len() {
            let Some((uc, ust)) = ud.best(i) else { continue };
            let at = start + i;
            if text.get(at) != Some(&'@') {
                continue;
            }
            let us = if building() { ud.text(i, ust) } else { String::new() };
            class_domain(text, at + 1, &keep, &mut |e, dc, ds| {
                if keep(e) && text[at + 1..e].contains(&'.') {
                    out.add_r(e, uc + dc, lz!("username: \"{us}\" domain: \"{ds}\""), ElR::Email(at as u32));
                }
            });
        }
    }
    let span = |e: usize| -> String { if building() { text[start..e].iter().collect() } else { String::new() } };
    // graph_domain: accepted+ ("." accepted{2,})+ at 0.1 − 0.0001
    let acc = |c: char| {
        is_alpha(c) || is_digit(c) || (c != '.' && c != '$' && c != ',' && SYMBOL_CHARS.binary_search(&c).is_ok())
    };
    let mut i = start;
    while i < text.len() && acc(text[i]) {
        i += 1;
    }
    out.set_branch(2);
    if i > start && branch_on(2) {
        while text.get(i) == Some(&'.') {
            let mut j = i + 1;
            while j < text.len() && acc(text[j]) {
                j += 1;
                if j - i - 1 >= 2 && keep(j) {
                    out.add_r(j, cost(0.1) + NEG, lz!("domain: \"{}\"", span(j)), ElR::Span);
                }
            }
            if j - i - 1 < 2 {
                break;
            }
            i = j;
        }
    }
    // protocol + domain
    out.set_branch(3);
    for (k, (pe, ps, pc)) in protocols(text, start).into_iter().enumerate().filter(|_| branch_on(3)) {
        class_domain(text, pe, &keep, &mut |e, dc, ds| {
            if !keep(e) {
                return;
            }
            out.add_r(e, pc + NEG + dc, lz!("protocol: \"{ps}\" domain: \"{ds}\""), ElR::Proto(k as u8));
        });
    }
    // ALPHA+ - "and", (" "? "/" " "? ALPHA+)+
    let mut a = start;
    while a < text.len() && is_alpha(text[a]) {
        a += 1;
    }
    out.set_branch(4);
    if a > start && text[start..a] != ['a', 'n', 'd'] && branch_on(4) {
        let mut frontier = vec![a];
        while let Some(p) = frontier.pop() {
            let mut q = p;
            if text.get(q) == Some(&' ') {
                q += 1;
            }
            if text.get(q) != Some(&'/') {
                continue;
            }
            q += 1;
            for r in [q, q + 1] {
                if r == q + 1 && text.get(q) != Some(&' ') {
                    continue;
                }
                let mut e = r;
                while e < text.len() && is_alpha(text[e]) {
                    e += 1;
                }
                if e > r {
                    // all prefixes of the alpha run are accepted; the run end continues the closure
                    for k in (r + 1..=e).filter(|&k| keep(k)) {
                        out.add_r(k, 0, lz!("domain: \"{}\"", span(k)), ElR::Span);
                    }
                    frontier.push(e);
                }
            }
        }
    }
    // cc phrases
    out.set_branch(5);
    for (r, e) in CC_CUES.prefix_matches(text, start).filter(|_| branch_on(5)) {
        let mut d = e;
        while d < text.len() && is_digit(text[d]) && d - e < 16 {
            d += 1;
            if d - e >= 4 && keep(d) {
                let s: String = if building() { text[e..d].iter().collect() } else { String::new() };
                let rd = ElR::Cc(row_index(&CC_CUES, r), e as u32);
                out.add_r(d, r.cost + NEG, lz!("protocol: \"{}\" domain: \"{s}\"", r.value), rd);
            }
        }
    }
}

pub struct Electronic;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 4;

impl Classifier for Electronic {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // every branch needs "@", ".", "/" in the token, or starts with a credit-card cue
        let mut e = start;
        let mut hit = false;
        while e < text.len() && !is_nemo_space(text[e]) {
            hit |= matches!(text[e], '@' | '.' | '/');
            e += 1;
        }
        // slash_string allows " / " after the first word
        hit |= text.get(e) == Some(&' ') && text.get(e + 1) == Some(&'/');
        if !hit && CC_CUES.prefix_matches(text, start).next().is_none() {
            return;
        }
        let mut o = Outs::default();
        electronic_spans(text, start, &|e| crate::lang::en::lazy::keep_end(text, e), &mut o);
        for ((e, c, s), (b, r)) in o.into_entries() {
            if !crate::lang::en::lazy::keep_end(text, e) {
                continue;
            }
            let cost = cost(1.11) + c;
            crate::lang::en::lazy::record(MEMBER, start, e, cost, b, Reading::Electronic(r));
            out.push(Candidate { end: e, cost, rank: 0, tag: lz!("electronic {{ {s} }}") });
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let end = candidate.end;
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::Electronic(r) => Some(format!("electronic {{ {} }}", fields(text, start, end, r))),
            _ => None,
        })
    }
}

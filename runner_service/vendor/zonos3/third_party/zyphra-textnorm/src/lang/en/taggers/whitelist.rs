//! `en/taggers/whitelist.py` (deterministic, cased, no input file) and `roman.py::get_names`.

use std::sync::LazyLock;

use crate::lang::en::graph_utils::row_index;
use crate::lang::en::lazy::{Reading, building};
use crate::lattice::{Candidate, Classifier};
use crate::text::{Table, cost, is_nemo_space, is_upper, match_str};

crate::labels!(TTS, "en/whitelist/tts.tsv");
crate::labels!(SYMBOL, "en/whitelist/symbol.tsv");
crate::labels!(STATES, "en/address/state.tsv");
crate::labels!(MALE, "en/roman/male.tsv");
crate::labels!(FEMALE, "en/roman/female.tsv");

fn pairs(rows: &[Vec<String>]) -> Table {
    Table::from_pairs(rows.iter().filter(|r| r.len() >= 2).map(|r| (r[0].clone(), r[1].clone())))
}

static TTS_T: LazyLock<Table> = LazyLock::new(|| pairs(&TTS));
static SYMBOL_T: LazyLock<Table> =
    LazyLock::new(|| pairs(&SYMBOL.iter().filter(|r| r[0] != "/").cloned().collect::<Vec<_>>()));

/// `get_names()`: male and female names plus their uppercase forms (identity map).
pub static NAMES: LazyLock<Table> = LazyLock::new(|| {
    let mut v = Vec::new();
    for t in [&*MALE, &*FEMALE] {
        for r in t.iter() {
            v.push((r[0].clone(), r[0].clone()));
        }
        for r in t.iter() {
            v.push((r[0].to_uppercase(), r[0].to_uppercase()));
        }
    }
    Table::from_pairs(v)
});

/// Inverted `state.tsv` (abbr → name) with the `X.Y` abbreviation variants.
pub static STATE_INV: LazyLock<Table> = LazyLock::new(|| {
    let mut v: Vec<(String, String)> = STATES.iter().map(|r| (r[1].clone(), r[0].clone())).collect();
    for r in STATES.iter() {
        let y = &r[1];
        let mut it = y.chars();
        let first = it.next().unwrap();
        v.push((format!("{first}.{}", it.as_str()), r[0].clone()));
    }
    Table::from_pairs(v)
});

/// The first two chars of every `tts.tsv`/`symbol.tsv` key (one-char keys as `(c, '\0')`), sorted: a key matches at
/// `start` only if its pair does, which rejects most word starts before the trie walk.
static FIRST_PAIRS: LazyLock<Vec<(char, char)>> = LazyLock::new(|| {
    let mut v: Vec<(char, char)> = TTS_T
        .rows()
        .iter()
        .chain(SYMBOL_T.rows())
        .filter_map(|r| {
            let mut it = r.key.chars();
            Some((it.next()?, it.next().unwrap_or('\0')))
        })
        .collect();
    v.sort_unstable();
    v.dedup();
    v
});

/// [`FIRST_PAIRS`] with an ASCII first char as a bitmap: bit `n` of row `c` for the pair `(c, n)` (`n` ASCII; bit 0
/// for a one-char key), bit 128 for any non-ASCII second char (then [`FIRST_PAIRS`] decides).
static ASCII_PAIRS: LazyLock<Vec<[u64; 3]>> = LazyLock::new(|| {
    let mut t = vec![[0u64; 3]; 128];
    for &(c, n) in FIRST_PAIRS.iter().filter(|p| p.0.is_ascii()) {
        let bit = if n.is_ascii() { n as usize } else { 128 };
        t[c as usize][bit / 64] |= 1 << (bit % 64);
    }
    t
});

/// Whether some `tts.tsv`/`symbol.tsv` key can match at `start`.
fn table_start(text: &[char], start: usize) -> bool {
    let c = text[start];
    let next = text.get(start + 1).copied();
    let pairs = &*FIRST_PAIRS;
    let found = |n: char| pairs.binary_search(&(c, n)).is_ok();
    if !c.is_ascii() {
        return found('\0') || next.is_some_and(found);
    }
    let row = &ASCII_PAIRS[c as usize];
    let has = |bit: usize| row[bit / 64] >> (bit % 64) & 1 == 1;
    has(0)
        || match next {
            Some(n) if n.is_ascii() => has(n as usize),
            Some(n) => has(128) && found(n),
            None => false,
        }
}

/// One `WhiteListFst.graph` output at a start: the row it takes from its table, or the positions it copies.
#[derive(Clone, Copy, Debug)]
pub enum WlR {
    /// `symbol.tsv` row.
    Symbol(u16),
    /// `Saint` + `NAMES` row.
    Saint(u16),
    /// The capitals of an initialism ending at `.1`, separated by `.0` + 1 chars.
    Initials(u8, u32),
    /// `tts.tsv` row.
    Tts(u16),
    /// `text[start..i]`, the comma (`gap` 2: and a space), the `STATE_INV` row: `(i, gap, row)`.
    State(u32, u8, u16),
}

/// The output of `r` at `start` (spaces not yet converted).
fn output(text: &[char], start: usize, r: WlR) -> String {
    match r {
        WlR::Symbol(row) => SYMBOL_T.rows()[row as usize].value.clone(),
        WlR::Saint(row) => format!("Saint {}", NAMES.rows()[row as usize].value),
        WlR::Initials(sl, i) => text[start..i as usize].iter().step_by(sl as usize + 1).collect(),
        WlR::Tts(row) => TTS_T.rows()[row as usize].value.clone(),
        WlR::State(i, gap, row) => {
            let mut o: String = text[start..i as usize].iter().collect();
            o.push_str(if gap == 2 { ", " } else { "," });
            o.push_str(&STATE_INV.rows()[row as usize].value);
            o
        }
    }
}

/// Every `(end, output reading)` of `WhiteListFst.graph` from `start` (outputs by [`output`]).
pub fn whitelist_spans(text: &[char], start: usize, keep: &dyn Fn(usize) -> bool, f: &mut dyn FnMut(usize, WlR)) {
    let mut f = |e: usize, r: WlR| {
        if keep(e) {
            f(e, r)
        }
    };
    let table_start = table_start(text, start);
    // tie (`~`: tts "approximately" vs symbol "tilde"): the oracle picks the symbol row
    if table_start {
        for (r, e) in SYMBOL_T.prefix_matches(text, start) {
            f(e, WlR::Symbol(row_index(&SYMBOL_T, r)));
        }
    }
    // st/St/ST "."* " " name
    for p in ["st", "St", "ST"].into_iter().filter(|_| matches!(text[start], 's' | 'S')) {
        if let Some(l) = match_str(text, start, p) {
            let mut i = start + l;
            while text.get(i) == Some(&'.') {
                i += 1;
            }
            if text.get(i) == Some(&' ') {
                for (r, e) in NAMES.prefix_matches(text, i + 1) {
                    f(e, WlR::Saint(row_index(&NAMES, r)));
                }
            }
        }
    }
    // UPPER (x UPPER){2,} "."?
    if is_upper(text[start]) {
        for sep in [".", ". "] {
            let sl = sep.chars().count();
            let mut i = start + 1;
            let mut n = 1;
            loop {
                if match_str(text, i, sep).is_none() || !text.get(i + sl).is_some_and(|&c| is_upper(c)) {
                    break;
                }
                i += sl + 1;
                n += 1;
                if n >= 3 {
                    // the letters at start, start + sl + 1, ... (every (sl + 1)-th char)
                    let r = WlR::Initials(sl as u8, i as u32);
                    f(i, r);
                    if text.get(i) == Some(&'.') {
                        f(i + 1, r);
                    }
                }
            }
        }
    }
    // tts last: equal-cost ties go to the symbol table (`~` → tilde) and to the initialism branch
    // (`A.D.A.M.` → ADAM), as the oracle picks
    if table_start {
        for (r, e) in TTS_T.prefix_matches(text, start) {
            f(e, WlR::Tts(row_index(&TTS_T, r)));
        }
    }
    // NOT_SPACE+ (", " | ",") state: every state key starts with an uppercase ASCII letter
    let mut i = start;
    while i < text.len() && !is_nemo_space(text[i]) {
        i += 1;
        if text.get(i) != Some(&',') {
            continue;
        }
        let k = if text.get(i + 1) == Some(&' ') { i + 2 } else { i + 1 };
        if !text.get(k).is_some_and(|&c| is_upper(c)) {
            continue;
        }
        for gap in [1usize, 2] {
            if gap == 2 && text.get(i + 1) != Some(&' ') {
                continue;
            }
            for (r, e) in STATE_INV.prefix_matches(text, i + gap) {
                f(e, WlR::State(i as u32, gap as u8, row_index(&STATE_INV, r)));
            }
        }
    }
}

pub struct WhiteList;

/// This member's id in the branch stash (`lazy::record`).
const MEMBER: u8 = 9;

/// The `name: "…"` tag of `r` at `start` (spaces as NBSP).
fn tag(text: &[char], start: usize, r: WlR) -> String {
    let o = output(text, start, r);
    let mut tag = String::with_capacity(o.len() + 10);
    tag.push_str("name: \"");
    tag.extend(o.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
    tag.push('"');
    tag
}

impl Classifier for WhiteList {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let build = building();
        whitelist_spans(text, start, &|e| crate::lang::en::lazy::keep_end(text, e), &mut |e, r| {
            crate::lang::en::lazy::record(MEMBER, start, e, cost(1.01), 0, Reading::WhiteList(r));
            let tag = if build { tag(text, start, r) } else { String::new() };
            out.push(Candidate { end: e, cost: cost(1.01), rank: 0, tag });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        crate::lang::en::lazy::materialize_recorded(self, MEMBER, &[], text, start, candidate, |r| match r {
            Reading::WhiteList(r) => Some(tag(text, start, r)),
            _ => None,
        })
    }
}

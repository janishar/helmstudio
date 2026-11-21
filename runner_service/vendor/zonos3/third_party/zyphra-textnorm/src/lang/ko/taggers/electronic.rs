//! `ko/taggers/electronic.py` (deterministic). Every branch but the protocol/cc ones copies its input, so spans
//! are enumerated as end positions and the tag is built from the input slice.

use std::sync::LazyLock;

use crate::lang::ko::graph_utils::*;
use crate::text::{Cost, Table, cost, is_nemo_space, match_str};

static SYMBOL: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("electronic/symbol.tsv")));
static EXT: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("electronic/extensions.tsv")));
static CC_CUES: LazyLock<Table> = LazyLock::new(|| Table::parse(data!("electronic/cc_cues.tsv")));

fn alnum(c: char) -> bool {
    c.is_ascii_alphanumeric()
}

fn username_char(c: char) -> bool {
    alnum(c) || (c != '@' && SYMBOL.contains_key(c.encode_utf8(&mut [0; 4])))
}

/// Ends of `tld+` chains starting at `q` (each `tld = "." + ALNUM{2,}`).
fn tld_chain(t: &[char], q: usize, out: &mut Vec<usize>) {
    let mut front = vec![q];
    while let Some(p) = front.pop() {
        if t.get(p) != Some(&'.') {
            continue;
        }
        for e in p + 3..=run(t, p + 1, alnum) {
            if !out.contains(&e) {
                out.push(e);
                front.push(e);
            }
        }
    }
}

/// Ends of `domain_core = (label + tld+) | tld`.
fn domain_core(t: &[char], p: usize) -> Vec<usize> {
    let mut ends = Vec::new();
    let l = run(t, p, |c| alnum(c) || c == '-');
    for le in p + 1..=l {
        tld_chain(t, le, &mut ends);
    }
    // the bare `| tld` branch: exactly one tld
    if t.get(p) == Some(&'.') {
        ends.extend(p + 3..=run(t, p + 1, alnum));
    }
    ends.sort_unstable();
    ends.dedup();
    ends
}

/// Ends of `domain_core + optional path`.
fn domain_with_path(t: &[char], p: usize) -> Vec<usize> {
    let core = domain_core(t, p);
    let mut ends = core.clone();
    for &e in &core {
        if t.get(e) == Some(&'/') {
            ends.extend(e + 2..=run(t, e + 1, |c| !is_nemo_space(c)));
        }
    }
    ends.sort_unstable();
    ends.dedup();
    ends
}

fn slice(t: &[char], a: usize, b: usize) -> String {
    t[a..b].iter().collect()
}

/// `protocol_symbols` on a literal (`"://"` etc.): each char → its symbol word (or `:` → `colon`) + " ".
fn symbol_outputs(lit: &str) -> Vec<String> {
    let mut outs = vec![String::new()];
    for c in lit.chars() {
        let mut opts: Vec<String> = SYMBOL.exact(c.encode_utf8(&mut [0; 4])).map(|r| r.value.clone()).collect();
        if c == ':' {
            opts.push("colon".into());
        }
        outs = outs.iter().flat_map(|o| opts.iter().map(move |x| format!("{o}{x} "))).collect();
    }
    outs
}

static SYM_START: LazyLock<Vec<String>> = LazyLock::new(|| symbol_outputs("://"));
static SYM_FILE: LazyLock<Vec<String>> = LazyLock::new(|| symbol_outputs(":///"));
static SYM_DOT: LazyLock<Vec<String>> = LazyLock::new(|| symbol_outputs("."));

/// `protocol` alternatives: `(end, value, cost)`.
fn protocol(t: &[char], p: usize) -> Vec<(usize, String, Cost)> {
    let mut r = Vec::new();
    if let Some(k) = match_str(t, p, "file:///") {
        for s in SYM_FILE.iter() {
            r.push((p + k, format!("file {s}"), 0));
        }
    }
    let mut starts = Vec::new();
    for (lit, out) in [("https://", "HTTPS "), ("http://", "HTTP ")] {
        if let Some(k) = match_str(t, p, lit) {
            for s in SYM_START.iter() {
                starts.push((p + k, format!("{out}{s}")));
            }
        }
    }
    for (e, s) in &starts {
        r.push((*e, s.clone(), 0));
    }
    let www = |q: usize| -> Vec<(usize, String)> {
        match match_str(t, q, "www.") {
            Some(k) => SYM_DOT.iter().map(|s| (q + k, format!("WWW {s}"))).collect(),
            None => Vec::new(),
        }
    };
    for (e, s) in www(p) {
        r.push((e, s, cost(-1000.0)));
    }
    for (e, s) in &starts {
        for (e2, s2) in www(*e) {
            r.push((e2, format!("{s}{s2}"), cost(-1000.0)));
        }
    }
    r
}

/// The chars a card-cue span can start with. Every other electronic span has a `.` in its non-space run (domains,
/// extensions, protocols) or starts with a four-digit group (card numbers); the union's gate tests those.
pub fn lead_chars() -> Vec<char> {
    CC_CUES.rows().iter().filter_map(|r| r.key.chars().next()).collect()
}

pub fn tag(t: &[char], p: usize) -> Outs {
    let mut r = Outs::new();
    let Some(&c0) = t.get(p) else { return r };
    // every branch but the cc ones needs a "." in the non-space run (domains, extensions, "://", "www.")
    let ns = run(t, p, |c| !is_nemo_space(c));
    let dotted = t[p..ns].contains(&'.');
    let cc = c0.is_ascii_digit() || CC_CUES.prefix_matches(t, p).next().is_some();
    if !dotted {
        if cc {
            cc_branches(t, p, &mut r);
        }
        return r;
    }
    // email: username + "@" + domain_with_path (guard holds whenever this parses: the domain contains ".")
    if alnum(c0) {
        let u = run(t, p + 1, username_char);
        for ue in p + 1..=u {
            if t.get(ue) == Some(&'@') {
                for e in domain_with_path(t, ue + 1) {
                    let tag = format!("username: \"{}\" domain: \"{}\"", slice(t, p, ue), slice(t, ue + 1, e));
                    add(&mut r, e, tag, 0);
                }
            }
        }
    }
    // graph_domain
    for e in domain_core(t, p) {
        add(&mut r, e, format!("domain: \"{}\"", slice(t, p, e)), 0);
    }
    // file_with_extension
    let stem = run(t, p, |c| !is_nemo_space(c) && c != '/' && c != '.');
    if stem > p && t.get(stem) == Some(&'.') {
        for (_, e) in EXT.prefix_matches(t, stem) {
            add(&mut r, e, format!("domain: \"{}\"", slice(t, p, e)), 0);
        }
    }
    // protocol + " " + domain_with_path
    if matches!(c0, 'h' | 'f' | 'w') {
        for (q, pv, pc) in protocol(t, p) {
            for e in domain_with_path(t, q) {
                add(&mut r, e, format!("protocol: \"{pv}\" domain: \"{}\"", slice(t, q, e)), pc);
            }
        }
    }
    cc_branches(t, p, &mut r);
    r
}

/// `cc_phrases | cc16_no_cue`.
fn cc_branches(t: &[char], p: usize, r: &mut Outs) {
    // cc_phrases
    for (row, e) in CC_CUES.prefix_matches(t, p) {
        let q = run(t, e, is_nemo_space);
        for k in e..=q {
            let de = run(t, k, is_digit);
            for d in k + 4..=de.min(k + 16) {
                add(r, d, format!("protocol: \"{}\" domain: \"{}\"", row.value, slice(t, k, d)), row.cost);
            }
        }
    }
    // cc16_no_cue (-1.0): (four + sep→" ")^3 + four + delete_space
    let mut q = p;
    let mut ok = true;
    for g in 0..4 {
        if q + 4 > t.len() || !t[q..q + 4].iter().all(char::is_ascii_digit) {
            ok = false;
            break;
        }
        q += 4;
        if g < 3 {
            if matches!(t.get(q), Some('-' | ' ')) {
                q += 1;
            } else {
                ok = false;
                break;
            }
        }
    }
    if ok {
        let digits: String = t[p..q].iter().map(|&c| if c == '-' { ' ' } else { c }).collect();
        let tag = format!("protocol: \"신용카드 \" domain: \"{digits}\"");
        for e in q..=run(t, q, is_nemo_space) {
            add(r, e, tag.clone(), cost(-1.0));
        }
    }
}

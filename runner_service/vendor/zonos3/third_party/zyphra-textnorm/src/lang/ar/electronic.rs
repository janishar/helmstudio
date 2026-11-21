//! Source Arabic electronic graphs: email, protocol URLs, known domains and IPv4.
use crate::lang::common::Fields;
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;
use std::sync::LazyLock;

crate::tsv!(DOMAINS, "ar/electronic/domain.tsv");
crate::tsv!(PROTOCOLS, "ar/electronic/protocol.tsv");
crate::tsv!(SYMBOLS, "ar/electronic/symbols.tsv");

const DIGITS: [&str; 10] = ["صفر", "واحد", "اثنان", "ثلاثة", "أربعة", "خمسة", "ستة", "سبعة", "ثمانية", "تسعة"];

// load_labels includes '#', whereas string_file treats that row as a comment.
static SYMBOL_INPUTS: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut inputs = [false; 128];
    for line in include_str!("../../../data/ar/electronic/symbols.tsv").lines() {
        if let Some((key, _)) = line.split_once('\t') {
            if key.len() == 1 && key.is_ascii() {
                inputs[key.as_bytes()[0] as usize] = true;
            }
        }
    }
    inputs
});
static PROTOCOL_ORDER: LazyLock<Vec<&'static crate::text::Row>> = LazyLock::new(|| {
    let mut rows: Vec<_> = PROTOCOLS.rows().iter().collect();
    rows.sort_by_key(|row| std::cmp::Reverse(row.key.len()));
    rows
});

fn symbol(c: char) -> bool {
    c.is_ascii() && SYMBOL_INPUTS[c as usize]
}
fn label_char(c: char) -> bool {
    c.is_ascii_alphanumeric() || matches!(c, '-' | '_')
}
fn starts(text: &[char], at: usize, key: &str) -> bool {
    let mut input = text[at..].iter();
    key.chars().all(|c| input.next() == Some(&c))
}

fn domains(text: &[char], start: usize, mut emit: impl FnMut(usize)) {
    let mut label_start = start;
    let mut dots = 0;
    let mut alpha = true;
    for i in start..text.len() {
        let c = text[i];
        if label_char(c) {
            alpha &= c.is_ascii_alphabetic();
            if dots > 0 && alpha && i + 1 - label_start >= 2 {
                emit(i + 1);
            }
        } else if c == '.' && i > label_start {
            dots += 1;
            label_start = i + 1;
            alpha = true;
        } else {
            break;
        }
    }
}

fn ipv4(text: &[char], start: usize, mut emit: impl FnMut(usize)) {
    let mut octet = 0;
    let mut digits = 0;
    for i in start..text.len() {
        if text[i].is_ascii_digit() && digits < 3 {
            digits += 1;
            if octet == 3 {
                emit(i + 1);
            }
        } else if text[i] == '.' && digits > 0 && octet < 3 {
            octet += 1;
            digits = 0;
        } else {
            break;
        }
    }
}

fn path(text: &[char], start: usize, emit: &mut impl FnMut(usize)) {
    if text.get(start) != Some(&'/') {
        return;
    }
    for i in start..text.len() {
        let c = text[i];
        if c == '/' || c == '.' || c.is_ascii_alphanumeric() || (symbol(c) && c != '@') {
            emit(i + 1);
        } else {
            break;
        }
    }
}

fn suffixes(text: &[char], end: usize, mut emit: impl FnMut(usize)) {
    emit(end);
    path(text, end, &mut emit);
    if text.get(end) == Some(&':') {
        for i in end + 1..(end + 6).min(text.len()) {
            if !text[i].is_ascii_digit() {
                break;
            }
            emit(i + 1);
            path(text, i + 1, &mut emit);
        }
    }
}

#[derive(Clone, Copy)]
enum Reading {
    Protocol(&'static str, usize),
    Email(usize),
    Domain,
}

fn scan(text: &[char], start: usize, mut emit: impl FnMut(usize, i32, Reading)) {
    if start >= text.len() {
        return;
    }
    for row in PROTOCOL_ORDER.iter() {
        if !starts(text, start, &row.key) {
            continue;
        }
        let host = start + row.key.len();
        let mut accept = |end| suffixes(text, end, |end| emit(end, 0, Reading::Protocol(&row.key, host)));
        domains(text, host, &mut accept);
        ipv4(text, host, &mut accept);
        if starts(text, host, "localhost") {
            accept(host + 9);
        }
    }
    for i in start..text.len() {
        let c = text[i];
        if c == '@' {
            if i > start {
                domains(text, i + 1, |end| emit(end, 1, Reading::Email(i)));
            }
            break;
        }
        if !c.is_ascii_alphanumeric() && !symbol(c) {
            break;
        }
    }
    let mut label_start = start;
    for i in start..text.len() {
        if label_char(text[i]) {
            for (_, end) in DOMAINS.prefix_matches(text, i + 1) {
                suffixes(text, end, |end| emit(end, 2, Reading::Domain));
            }
        } else if text[i] == '.' && i > label_start {
            label_start = i + 1;
        } else {
            break;
        }
    }
    ipv4(text, start, |end| suffixes(text, end, |end| emit(end, 3, Reading::Domain)));
}

pub struct ElectronicClassifier;
impl Classifier for ElectronicClassifier {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        scan(text, start, |end, rank, _| out.push(Candidate { end, rank, cost: cost(1.11), tag: String::new() }));
    }
    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut selected = None;
        scan(text, start, |end, rank, reading| {
            if selected.is_none() && end == candidate.end && rank == candidate.rank {
                selected = Some(reading);
            }
        });
        let reading = selected.expect("selected electronic graph path");
        let mut tag = String::with_capacity(candidate.end - start + 96);
        tag.push_str("electronic { ");
        let domain = match reading {
            Reading::Protocol(protocol, domain) => {
                tag.push_str("protocol: \"");
                tag.push_str(protocol);
                tag.push_str("\" ");
                domain
            }
            Reading::Email(at) => {
                tag.push_str("username: \"");
                tag.extend(text[start..at].iter());
                tag.push_str("\" ");
                at + 1
            }
            Reading::Domain => start,
        };
        tag.push_str("domain: \"");
        tag.extend(text[domain..candidate.end].iter());
        tag.push_str("\" preserve_order: true }");
        candidate.tag = tag;
    }
}

fn append_word(out: &mut String, word: &str) {
    if !out.is_empty() && !out.ends_with(' ') {
        out.push(' ');
    }
    for c in word.chars() {
        if c != ' ' || !out.ends_with(' ') {
            out.push(c);
        }
    }
    if !out.ends_with(' ') {
        out.push(' ');
    }
}

fn spell(s: &str) -> String {
    let mut out = String::with_capacity(s.len());
    let mut i = 0;
    while i < s.len() {
        let rest = &s[i..];
        if rest.starts_with('.') {
            if let Some(row) = DOMAINS.rows().iter().find(|row| {
                rest.starts_with(&row.key)
                    && (rest.len() == row.key.len()
                        || matches!(rest.as_bytes()[row.key.len()], b'/' | b':' | b'?' | b'#'))
            }) {
                append_word(&mut out, &row.value);
                i += row.key.len();
                continue;
            }
        }
        let c = rest.chars().next().expect("nonempty electronic suffix");
        if c.is_ascii_digit() {
            append_word(&mut out, DIGITS[(c as u8 - b'0') as usize]);
        } else {
            let mut buf = [0u8; 4];
            if let Some(row) = SYMBOLS.exact(c.encode_utf8(&mut buf)).next() {
                append_word(&mut out, &row.value);
            } else if c != ' ' || (!out.is_empty() && !out.ends_with(' ')) {
                out.push(c);
            }
        }
        i += c.len_utf8();
    }
    out.truncate(out.trim_end_matches(' ').len());
    out
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let username = f.take("username");
    let protocol = if username.is_none() { f.take("protocol") } else { None };
    let domain = f.take("domain")?;
    f.preserve_order();
    if !f.done() || domain.is_empty() || domain.contains('"') {
        return None;
    }
    let spoken_domain = spell(domain);
    if let Some(user) = username {
        if user.is_empty() || user.contains('"') {
            return None;
        }
        return Some(format!("{} آت {spoken_domain}", spell(user)));
    }
    if let Some(protocol) = protocol {
        let row = PROTOCOLS.exact(protocol).next()?;
        return Some(format!("{} {spoken_domain}", row.value));
    }
    Some(spoken_domain)
}

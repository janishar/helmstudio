//! `it/taggers/electronic.py` (deterministic).

use std::sync::LazyLock;

use crate::lattice::{Candidate, Classifier};
use crate::text::{Cost, starts_with};

crate::labels!(SYMBOLS, "it/electronic/symbols.tsv");

/// `all_characters` membership per ASCII code: alpha, digit, first column of `symbols.tsv` (all ASCII).
static ALL: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut t = [false; 128];
    for (i, x) in t.iter_mut().enumerate() {
        *x = (i as u8).is_ascii_alphanumeric();
    }
    for c in SYMBOLS.iter().filter_map(|r| r.first()).filter_map(|s| s.chars().next()) {
        if c.is_ascii() {
            t[c as usize] = true;
        }
    }
    t
});

/// `all_characters` member.
#[inline]
fn all(c: char) -> bool {
    c.is_ascii() && ALL[c as usize]
}

/// `accepted_characters` member: `all_characters` except `.`.
#[inline]
fn acc(c: char) -> bool {
    c != '.' && all(c)
}

/// Ends of `accepted+ ("." accepted+){min_dots,}` at `p`.
fn dotted(text: &[char], p: usize, need_dot: bool, mut f: impl FnMut(usize)) {
    let mut i = p;
    let mut dots = 0;
    while i < text.len() {
        let c = text[i];
        if acc(c) {
            i += 1;
            if dots > 0 || !need_dot {
                f(i);
            }
        } else if c == '.' && i > p && acc(text[i - 1]) {
            dots += 1;
            i += 1;
        } else {
            break;
        }
    }
}

pub struct Electronic {
    pub cost: Cost,
}

impl Electronic {
    fn push(&self, out: &mut Vec<Candidate>, end: usize, rank: i32, fields: &[(&str, &[char])]) {
        let mut tag = String::from("electronic { ");
        for (k, v) in fields {
            tag.push_str(k);
            tag.push_str(": \"");
            tag.extend(v.iter());
            tag.push_str("\" ");
        }
        tag.push_str("preserve_order: true }");
        out.push(Candidate { end, cost: self.cost, rank, tag });
    }
}

impl Classifier for Electronic {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        // every branch needs a `.` (domain) or `@` (email, tag) inside its run of `all_characters`
        let mut k = start;
        while k < text.len() && all(text[k]) && text[k] != '.' && text[k] != '@' {
            k += 1;
        }
        if !matches!(text.get(k), Some('.' | '@')) {
            return;
        }
        // domain_graph
        dotted(text, start, true, |e| self.push(out, e, 1, &[("domain", &text[start..e])]));
        // url = protocol + " " + domain_graph. Equal-cost ranks read off the oracle: a protocol without `www.`
        // beats the bare domain that also spells it (`https://www.nvidia.com` → protocol `https://`); a protocol
        // ending in `www.` loses to that unless the next label is one char (`www.voi.de` → bare domain,
        // `www.a.com` → protocol `www.`, `http://www.a.com` → protocol `http://www.`).
        for proto in ["https://", "http://", "www.", "https://www.", "http://www."] {
            if starts_with(text, start, proto) {
                let p = start + proto.chars().count();
                let rank = if !proto.ends_with("www.") {
                    0
                } else if text.get(p + 1) == Some(&'.') {
                    -1
                } else {
                    2
                };
                dotted(text, p, true, |e| {
                    self.push(out, e, rank, &[("protocol", &text[start..p]), ("domain", &text[p..e])])
                });
            }
        }
        // email = all_characters+ "@" domain_graph
        let mut k = start;
        while k < text.len() && all(text[k]) {
            k += 1;
        }
        if k > start && text.get(k) == Some(&'@') {
            dotted(text, k + 1, true, |e| {
                self.push(out, e, 0, &[("username", &text[start..k]), ("domain", &text[k + 1..e])])
            });
        }
        // tag = "@" (accepted+ | accepted+ ("." accepted+)+)
        if text[start] == '@' {
            dotted(text, start + 1, false, |e| self.push(out, e, 0, &[("username", &text[start + 1..e])]));
        }
    }
}

// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese electronic classifier.

use super::super::graph_utils::{WEIGHT_ELECTRONIC, cardinal_cost, spell_cardinal_digits};
use crate::lattice::{Candidate, Classifier};
use crate::text::Cost;

pub struct Electronic;
crate::labels!(DOMAINS_LABELS, "vi/electronic/domain.tsv");

fn alnum(c: char) -> bool {
    c.is_ascii_alphanumeric()
}

fn starts(text: &[char], pos: usize, s: &str) -> Option<usize> {
    let mut end = pos;
    for c in s.chars() {
        if text.get(end) != Some(&c) {
            return None;
        }
        end += 1;
    }
    Some(end)
}

// Labels start and end with an ASCII alphanumeric; interior hyphens are allowed.
fn hosts(text: &[char], start: usize, mut f: impl FnMut(usize, usize)) {
    if !text.get(start).is_some_and(|c| alnum(*c)) {
        return;
    }
    let mut i = start;
    let mut dots = 0;
    let mut at_label_start = true;
    while i < text.len() {
        let c = text[i];
        if alnum(c) {
            at_label_start = false;
            i += 1;
            f(i, dots);
        } else if c == '-' && !at_label_start {
            i += 1;
        } else if c == '.' && !at_label_start && text[i - 1] != '-' {
            dots += 1;
            at_label_start = true;
            i += 1;
        } else {
            break;
        }
    }
}

fn path_char(c: char) -> bool {
    alnum(c) || matches!(c, '/' | '-' | '_' | '.' | '?' | '=' | '&' | '#' | '%' | '~' | ':' | '+')
}

fn url_ends(text: &[char], host_end: usize, mut f: impl FnMut(usize)) {
    let mut ports = [None; 6];
    ports[0] = Some(host_end);
    if text.get(host_end) == Some(&':') {
        let mut p = host_end + 1;
        for slot in &mut ports[1..] {
            if !text.get(p).is_some_and(char::is_ascii_digit) {
                break;
            }
            p += 1;
            *slot = Some(p);
        }
    }
    for pos in ports.into_iter().flatten() {
        f(pos);
        if text.get(pos) != Some(&'/') {
            continue;
        }
        let mut i = pos + 1;
        f(i);
        while i < text.len() && path_char(text[i]) {
            let c = text[i];
            i += 1;
            if alnum(c) || c == '/' {
                f(i);
            }
        }
    }
}

fn numeric_segments(
    text: &[char],
    start: usize,
    prefix: Option<char>,
    render: bool,
    mut f: impl FnMut(usize, String, Cost),
) {
    fn visit(
        text: &[char],
        start: usize,
        pos: usize,
        prefix: Option<char>,
        segments: usize,
        words: &str,
        weight: Cost,
        lengths: &mut [usize; 4],
        octets: bool,
        render: bool,
        f: &mut dyn FnMut(usize, String, Cost),
    ) {
        if segments == 4 {
            return;
        }
        let mut digits = [0u8; 4];
        let mut i = pos;
        for len in 1..=4 {
            let Some(&c) = text.get(i) else { break };
            if !c.is_ascii_digit() {
                break;
            }
            digits[len - 1] = c as u8;
            i += 1;
            let Some(c) = cardinal_cost(&digits[..len]) else { continue };
            let mut next = String::new();
            if render {
                next.push_str(words);
                if segments > 0 {
                    next.push_str(" chấm ");
                }
                spell_cardinal_digits(&digits[..len], &mut next);
            }
            lengths[segments] = len;
            let octet = octets
                && (len == 1 || digits[0] != b'0')
                && len <= 3
                && digits[..len].iter().fold(0u32, |n, d| n * 10 + (d - b'0') as u32) <= 255;
            let count = segments + 1;
            let bare_version = prefix.is_none() && count == 3 && lengths[..3].iter().all(|&n| n != 3);
            let ip = prefix.is_none()
                && count == 4
                && octet
                && !(lengths[0] <= 3 && lengths[1..4].iter().all(|&n| n == 3) && text[start] != '0');
            if (prefix.is_some() && count >= 2) || bare_version || ip {
                f(i, if render { format!("value: \"{}\"", next) } else { String::new() }, weight + c);
            }
            if text.get(i) == Some(&'.') {
                visit(text, start, i + 1, prefix, count, &next, weight + c, lengths, octet, render, f);
            }
        }
    }
    let initial = if render { prefix.map_or_else(String::new, |c| format!("{} ", c)) } else { String::new() };
    visit(text, start, start, prefix, 0, &initial, 0, &mut [0; 4], true, render, &mut f);
}

impl Electronic {
    fn scan_spans(text: &[char], start: usize, render: bool, mut f: impl FnMut(usize, String, Cost)) {
        if start >= text.len() {
            return;
        }
        if text[start].is_ascii_digit() {
            // A digit-led branch is a version/IP, a dotted host, or an
            // email address. Before its first dot or @ it can consume only
            // username/host characters; a colon or whitespace ends that domain.
            let mut pos = start;
            let mut possible = false;
            while let Some(&c) = text.get(pos) {
                if matches!(c, '.' | '@') {
                    possible = true;
                    break;
                }
                if !(c.is_ascii_alphanumeric() || matches!(c, '-' | '_' | '+')) {
                    break;
                }
                pos += 1;
            }
            if !possible {
                return;
            }
        }
        if text[start].is_ascii_alphabetic() {
            let mut alpha_end = start;
            while text.get(alpha_end).is_some_and(char::is_ascii_alphabetic) {
                alpha_end += 1;
            }
            // Every alphabetic branch must continue with a digit, a host or
            // username character, a hyphenated value, or a protocol colon.
            if !text
                .get(alpha_end)
                .is_some_and(|&c| c.is_ascii_digit() || matches!(c, '-' | '.' | '_' | '+' | ':' | '@'))
            {
                return;
            }
            if text.get(alpha_end) == Some(&'-') {
                let alpha: String = if render { text[start..alpha_end].iter().collect() } else { String::new() };
                let mut digits = [0u8; 4];
                let mut p = alpha_end + 1;
                for n in 1..=4 {
                    let Some(&ch) = text.get(p) else { break };
                    if !ch.is_ascii_digit() {
                        break;
                    }
                    digits[n - 1] = ch as u8;
                    p += 1;
                    let Some(c) = cardinal_cost(&digits[..n]) else { continue };
                    let mut value = String::new();
                    if render {
                        value.push_str(&alpha);
                        value.push(' ');
                        spell_cardinal_digits(&digits[..n], &mut value);
                    }
                    f(p, if render { format!("value: \"{}\"", value) } else { String::new() }, c);
                    if text.get(p) != Some(&'-') {
                        continue;
                    }
                    let mut q = p + 1;
                    let mut next_digits = [0u8; 4];
                    for m in 1..=4 {
                        let Some(&ch) = text.get(q) else { break };
                        if !ch.is_ascii_digit() {
                            break;
                        }
                        next_digits[m - 1] = ch as u8;
                        q += 1;
                        if let Some(c2) = cardinal_cost(&next_digits[..m]) {
                            let tag = if render {
                                let mut next = String::new();
                                spell_cardinal_digits(&next_digits[..m], &mut next);
                                format!("value: \"{} {}\"", value, next)
                            } else {
                                String::new()
                            };
                            f(q, tag, c + c2);
                        }
                    }
                    let mut q = p + 1;
                    while text.get(q).is_some_and(char::is_ascii_alphabetic) {
                        q += 1;
                        let tag = if render {
                            let tail: String = text[p + 1..q].iter().collect();
                            format!("value: \"{} {}\"", value, tail)
                        } else {
                            String::new()
                        };
                        f(q, tag, c);
                    }
                }
            }
        }
        if matches!(text[start], 'v' | 'V') {
            numeric_segments(text, start + 1, Some(text[start]), render, &mut f);
        }
        if text[start].is_ascii_digit() {
            numeric_segments(text, start, None, render, &mut f);
        }
        for proto in ["http://www.", "https://www.", "http://", "https://", "www."] {
            if let Some(host_start) = starts(text, start, proto) {
                hosts(text, host_start, |host_end, _| {
                    url_ends(text, host_end, |end| {
                        let tag = if render {
                            let domain: String = text[host_start..end].iter().collect();
                            format!("protocol: \"{}\" domain: \"{}\"", proto, domain)
                        } else {
                            String::new()
                        };
                        f(end, tag, 0);
                    });
                });
            }
        }
        if alnum(text[start]) {
            hosts(text, start, |host_end, dots| {
                if dots == 0 {
                    return;
                }
                let known = DOMAINS_LABELS.iter().any(|row| {
                    row.first().is_some_and(|suffix| {
                        let n = suffix.chars().count();
                        n < host_end - start && text[host_end - n..host_end].iter().copied().eq(suffix.chars())
                    })
                });
                if known {
                    url_ends(text, host_end, |end| {
                        let tag = if render {
                            let domain: String = text[start..end].iter().collect();
                            format!("domain: \"{}\"", domain)
                        } else {
                            String::new()
                        };
                        f(end, tag, 0);
                    });
                }
            });
            let mut user_end = start;
            while user_end < text.len() && (alnum(text[user_end]) || matches!(text[user_end], '.' | '_' | '-' | '+')) {
                user_end += 1;
            }
            if text.get(user_end) == Some(&'@') && user_end > start && alnum(text[user_end - 1]) {
                let host_start = user_end + 1;
                hosts(text, host_start, |host_end, dots| {
                    if dots > 0 {
                        let tag = if render {
                            let user: String = text[start..user_end].iter().collect();
                            let domain: String = text[host_start..host_end].iter().collect();
                            format!("username: \"{}\" domain: \"{}\"", user, domain)
                        } else {
                            String::new()
                        };
                        f(host_end, tag, 0);
                    }
                });
            }
        }
    }
}

impl Classifier for Electronic {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        Self::scan_spans(text, start, false, |end, _, weight| {
            out.push(Candidate { end, cost: WEIGHT_ELECTRONIC + weight, rank: 0, tag: String::new() });
        });
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        Self::scan_spans(text, start, true, |end, tag, weight| {
            if candidate.tag.is_empty() && end == candidate.end && WEIGHT_ELECTRONIC + weight == candidate.cost {
                candidate.tag = format!("electronic {{ {tag} }}");
            }
        });
    }
}

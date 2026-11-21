// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Electronic classifier, ported from ru/taggers/electronic.py.

use super::number::{Case, Gender, cardinal};
use super::whitelist::{LATIN_TO_CYRILLIC, is_ru_alpha};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

crate::labels!(RAW_SERVERS, "ru/electronic/server_name.tsv");
crate::labels!(RAW_DOMAINS, "ru/electronic/domain.tsv");
crate::labels!(RAW_SYMBOLS, "ru/electronic/symbols.tsv");

const URL_SYMBOLS: &[(&str, &str)] = &[
    (".", "точка"),
    ("/", "слэш"),
    ("-", "дефис"),
    ("_", "нижнее подчеркивание"),
    ("?", "вопросительный знак"),
    ("=", "равно"),
    ("&", "амперсанд"),
    ("#", "решетка"),
    (":", "двоеточие"),
    ("%", "процент"),
    ("~", "тильда"),
];
const TLD_LIST: &[&str] = &[
    "com", "ru", "org", "net", "io", "ai", "dev", "app", "edu", "gov", "co", "me", "рф", "su", "info", "by", "kz", "ua",
];

fn letter(c: char) -> Option<&'static str> {
    let c = c.to_ascii_lowercase();
    LATIN_TO_CYRILLIC.iter().find(|(ch, _, _)| *ch == c).map(|(_, value, _)| *value)
}

pub fn spell_char(c: char) -> String {
    if let Some(value) = letter(c) {
        return value.to_owned();
    }
    if is_ru_alpha(c) {
        return c.to_string();
    }
    if c.is_ascii_digit() {
        return cardinal((c as u8 - b'0') as u64, Gender::Masc, Case::Nom);
    }
    if let Some(row) = RAW_SYMBOLS.iter().find(|row| row.len() >= 2 && row[0].chars().eq(std::iter::once(c))) {
        return row[1].clone();
    }
    c.to_string()
}

pub(super) fn symbol(c: char) -> Option<&'static str> {
    RAW_SYMBOLS.iter().find(|row| row.len() >= 2 && row[0].chars().eq(std::iter::once(c))).map(|row| row[1].as_str())
}

// The final email spell transducer accepts Russian letters, spaces, and Latin
// letters mapped through TO_CYRILLIC. Its input need not itself be a word.
fn spellable(s: &str) -> bool {
    s.chars().all(|c| c == ' ' || is_ru_alpha(c) || (c.is_ascii_alphabetic() && letter(c).is_some()))
}

fn spell_into(s: &str, out: &mut String) {
    for c in s.chars() {
        if c.is_ascii_alphabetic() {
            if let Some(value) = letter(c) {
                out.push_str(value);
            }
        } else {
            out.push(c);
        }
    }
}

fn row_value(row: &[String]) -> &str {
    row.get(1).map_or(row[0].as_str(), String::as_str)
}

#[derive(Clone, Copy)]
struct EmailPath {
    weight: Cost,
    split: usize,
    server: Option<&'static str>,
    domain: Option<&'static str>,
}

// Find the first minimum-weight path through the two server and two domain
// branches. The split is a source state: different splits must all be tried.
fn email_path(s: &str) -> Option<EmailPath> {
    let (user, domain) = s.split_once('@')?;
    let mut chars = user.chars();
    if !chars.next()?.is_ascii_alphabetic() {
        return None;
    }
    let mut user_weight = 0;
    let mut symbols = false;
    for c in user.chars() {
        if c.is_ascii_alphabetic() {
            letter(c)?;
            user_weight += cost(1.1);
            symbols = false;
        } else if c.is_ascii_digit() {
            user_weight += cost(1.09);
            symbols = false;
        } else {
            let value = symbol(c)?;
            if !spellable(value) {
                return None;
            }
            if !symbols {
                user_weight += cost(1.09);
            }
            symbols = true;
        }
    }
    let bytes = domain.as_bytes();
    if bytes.len() < 2
        || !bytes[0].is_ascii_alphabetic()
        || !bytes[bytes.len() - 1].is_ascii_alphanumeric()
        || !bytes.iter().all(|c| c.is_ascii_alphanumeric() || *c == b'-' || *c == b'.')
    {
        return None;
    }
    let mut best: Option<EmailPath> = None;
    for split in 1..domain.len() {
        let server = &domain[..split];
        let suffix = &domain[split..];
        let mut servers: [Option<(Cost, Option<&'static str>)>; 2] = [None, None];
        if let Some(row) =
            RAW_SERVERS.iter().find(|row| !row.is_empty() && row[0] == server && spellable(row_value(row)))
        {
            servers[0] = Some((cost(1.09), Some(row_value(row))));
        }
        // server_default = alnum+ symbols* alnum+. Digits are spoken
        // before the spell transducer; alphabetic characters pass through it.
        let b = server.as_bytes();
        let mut i = 0;
        while i < b.len() && b[i].is_ascii_alphanumeric() {
            i += 1;
        }
        let left = i;
        while i < b.len() && (b[i] == b'.' || b[i] == b'-') {
            i += 1;
        }
        let right = i;
        while i < b.len() && b[i].is_ascii_alphanumeric() {
            i += 1;
        }
        let valid_default =
            if right == left { left >= 2 && left == b.len() } else { left > 0 && i == b.len() && i > right };
        if valid_default && server.chars().all(|c| !c.is_ascii_alphabetic() || letter(c).is_some()) {
            servers[1] = Some((cost(1.1), None));
        }
        let mut domains: [Option<(Cost, Option<&'static str>)>; 2] = [None, None];
        if let Some(key) = suffix.strip_prefix('.') {
            if let Some(row) =
                RAW_DOMAINS.iter().find(|row| !row.is_empty() && row[0] == key && spellable(row_value(row)))
            {
                domains[0] = Some((cost(1.09), Some(row_value(row))));
            }
        }
        if let Some((before, after)) = suffix.split_once('.') {
            if !after.is_empty()
                && before.chars().chain(after.chars()).all(|c| c.is_ascii_alphabetic() && letter(c).is_some())
            {
                domains[1] = Some((cost(1.1), None));
            }
        }
        for (sw, sv) in servers.into_iter().flatten() {
            for (dw, dv) in domains.into_iter().flatten() {
                let weight = user_weight + sw + dw;
                if best.as_ref().is_none_or(|old| weight < old.weight) {
                    best = Some(EmailPath { weight, split, server: sv, domain: dv });
                }
            }
        }
    }
    best
}

fn email(s: &str, out: Option<&mut String>) -> Option<Cost> {
    let path = email_path(s)?;
    if let Some(out) = out {
        let (user, domain) = s.split_once('@')?;
        for c in user.chars() {
            if c.is_ascii_alphabetic() {
                out.push_str(letter(c)?);
            } else if c.is_ascii_digit() {
                out.push_str(&spell_char(c));
            } else {
                spell_into(symbol(c)?, out);
            }
            out.push(' ');
        }
        out.push_str("собака ");
        let (server, suffix) = domain.split_at(path.split);
        if let Some(value) = path.server {
            spell_into(value, out);
            out.push(' ');
        } else {
            for c in server.chars() {
                if c.is_ascii_alphabetic() {
                    out.push_str(letter(c)?);
                } else if c.is_ascii_digit() {
                    out.push_str(&spell_char(c));
                } else {
                    spell_into(symbol(c)?, out);
                }
                out.push(' ');
            }
        }
        if let Some(value) = path.domain {
            out.push_str("точка ");
            spell_into(value, out);
        } else {
            let (before, after) = suffix.split_once('.')?;
            for c in before.chars() {
                out.push_str(letter(c)?);
                out.push(' ');
            }
            out.push_str("точка ");
            let mut first = true;
            for c in after.chars() {
                if !first {
                    out.push(' ');
                }
                out.push_str(letter(c)?);
                first = false;
            }
        }
    }
    Some(path.weight)
}

pub fn render_email(s: &str) -> Option<String> {
    let mut out = String::new();
    email(s, Some(&mut out))?;
    Some(out)
}

fn url_symbol(c: char) -> Option<&'static str> {
    URL_SYMBOLS.iter().find(|(key, _)| key.chars().eq(std::iter::once(c))).map(|(_, value)| *value)
}

// URL words are separated by symbol runs. A common server/domain word has
// weight -0.1; otherwise the whole alphanumeric run has weight 0.01.
fn url_body(s: &str, mut out: Option<&mut String>) -> Option<Cost> {
    let mut weight = 0;
    let mut first = true;
    let mut start = 0;
    for (pos, c) in s.char_indices().chain(std::iter::once((s.len(), '\0'))) {
        let sym = url_symbol(c);
        if sym.is_none() && pos != s.len() {
            continue;
        }
        let word = &s[start..pos];
        if !word.is_empty() {
            if !first {
                if let Some(dst) = out.as_deref_mut() {
                    dst.push(' ');
                }
            }
            first = false;
            let common = RAW_SERVERS.iter().chain(RAW_DOMAINS.iter()).find(|row| !row.is_empty() && row[0] == word);
            if let Some(row) = common {
                weight += cost(-0.1);
                if let Some(dst) = out.as_deref_mut() {
                    dst.push_str(row_value(row));
                }
            } else {
                if !word
                    .chars()
                    .all(|c| is_ru_alpha(c) || c.is_ascii_digit() || (c.is_ascii_alphabetic() && letter(c).is_some()))
                {
                    return None;
                }
                weight += cost(0.01);
                if let Some(dst) = out.as_deref_mut() {
                    for (i, c) in word.chars().enumerate() {
                        if i > 0 {
                            dst.push(' ');
                        }
                        if c.is_ascii_alphabetic() {
                            dst.push_str(letter(c)?);
                        } else if c.is_ascii_digit() {
                            dst.push_str(&spell_char(c));
                        } else {
                            dst.push(c);
                        }
                    }
                }
            }
        }
        if let Some(spoken) = sym {
            if first {
                return None;
            }
            if let Some(dst) = out.as_deref_mut() {
                dst.push(' ');
                dst.push_str(spoken);
            }
            start = pos + c.len_utf8();
        }
    }
    if first { None } else { Some(weight) }
}

fn bare_url(s: &str) -> bool {
    let host_end = s.find(['/', '?']).unwrap_or(s.len());
    let Some((name, tld)) = s[..host_end].split_once('.') else { return false };
    !name.is_empty()
        && name.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-')
        && TLD_LIST.contains(&tld)
        && s[host_end..].chars().all(|c| c.is_ascii_alphanumeric() || "/.-_?=&#%~:".contains(c))
}

fn url(s: &str, out: Option<&mut String>) -> Option<Cost> {
    let (prefix, rest, protocol) = if let Some(rest) = s.strip_prefix("https://") {
        ("эйч ти ти пи эс двоеточие слэш слэш ", rest, true)
    } else if let Some(rest) = s.strip_prefix("http://") {
        ("эйч ти ти пи двоеточие слэш слэш ", rest, true)
    } else if let Some(rest) = s.strip_prefix("www.") {
        ("дабл ю дабл ю дабл ю точка ", rest, false)
    } else {
        if !bare_url(s) {
            return None;
        }
        let weight = url_body(s, None)? + cost(0.5);
        if let Some(dst) = out {
            url_body(s, Some(dst))?;
        }
        return Some(weight);
    };
    // The protocol branch can consume an optional www. Both paths are
    // considered because www can also be an ordinary body word.
    let plain = url_body(rest, None);
    let with_www =
        if protocol { rest.strip_prefix("www.").and_then(|r| url_body(r, None).map(|w| (r, w))) } else { None };
    let (body, extra_www, weight) = match (plain, with_www) {
        (Some(a), Some((r, b))) if b < a => (r, true, b),
        (Some(a), _) => (rest, false, a),
        (None, Some((r, b))) => (r, true, b),
        _ => return None,
    };
    if let Some(dst) = out {
        dst.push_str(prefix);
        if extra_www {
            dst.push_str("дабл ю дабл ю дабл ю точка ");
        }
        url_body(body, Some(dst))?;
    }
    Some(weight)
}

pub fn render_url(s: &str) -> Option<String> {
    let mut out = String::new();
    url(s, Some(&mut out))?;
    Some(out)
}

pub fn render_electronic(s: &str) -> Option<String> {
    if s.contains('@') { render_email(s) } else { render_url(s) }
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let weight = if s.contains('@') { email(s, None) } else { url(s, None) };
    if let Some(weight) = weight {
        out.push(Candidate { end, cost: cost(1.1) + weight, rank: 0, tag: String::new() });
    }
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    if let Some(spoken) = render_electronic(s) {
        candidate.tag = format!("electronic {{ username: \"{spoken}\" }}");
    }
}

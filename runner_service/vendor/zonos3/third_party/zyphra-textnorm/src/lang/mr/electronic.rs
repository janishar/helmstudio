//! Electronic addresses, paths, domains and chemical formulae.
use super::numbers;
use crate::lang::common::Fields;
use crate::lattice::Candidate;
use crate::tag::Dict;
use crate::text::cost;

const SYMBOLS: &str = include_str!("../../../data/mr/electronic/symbols.tsv");
const CLASSES: &str = include_str!("../../../data/mr/electronic/symbol_classes.tsv");
const PROTOCOLS: &str = include_str!("../../../data/mr/electronic/protocols.tsv");
const SUB: &str = include_str!("../../../data/mr/electronic/subscript_digit.tsv");
const LETTERS: &str = include_str!("../../../data/mr/address/letters.tsv");
const EXT: &str = include_str!("../../../data/mr/electronic/file_extensions.tsv");
const ELEMENTS: &str = include_str!("../../../data/mr/electronic/elements.tsv");
const DOMAINS: &str = include_str!("../../../data/mr/electronic/domain.tsv");

fn rows(data: &'static str) -> impl Iterator<Item = (&'static str, &'static str)> {
    data.lines().filter_map(|l| l.trim_end_matches('\r').split_once('\t'))
}

fn table(data: &'static str, s: &str) -> Option<&'static str> {
    rows(data).find_map(|(k, v)| (k == s).then_some(v))
}

fn symbol(c: char, class: &str) -> bool {
    use std::collections::HashMap;
    use std::sync::LazyLock;
    static CLASSES_INDEX: LazyLock<HashMap<char, u8>> = LazyLock::new(|| {
        let mut index = HashMap::new();
        for (key, classes) in rows(CLASSES) {
            if let Some(ch) = key.chars().next() {
                let mut mask = 0;
                for class in classes.split(',') {
                    mask |= match class {
                        "email" => 1,
                        "url" => 2,
                        "unix" => 4,
                        "windows" => 8,
                        "chem" => 16,
                        _ => 0,
                    };
                }
                *index.entry(ch).or_insert(0) |= mask;
            }
        }
        index
    });
    let flag = match class {
        "email" => 1,
        "url" => 2,
        "unix" => 4,
        "windows" => 8,
        "chem" => 16,
        _ => 0,
    };
    CLASSES_INDEX.get(&c).is_some_and(|bits| bits & flag != 0)
}

fn alnum(c: char) -> bool {
    c.is_ascii_alphanumeric() || numbers::digit(c).is_some() || ('₀'..='₉').contains(&c)
}

fn emit(out: &mut Vec<Candidate>, end: usize, fields: impl FnOnce() -> String, c: f64) {
    out.push(Candidate {
        end,
        cost: cost(1.1 + c),
        rank: 10,
        tag: super::lazy::tag(|| format!("electronic {{ {} }}", fields())),
    });
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    // 1. Email
    if let Some((u, d)) = s.split_once('@') {
        if !u.is_empty()
            && !d.is_empty()
            && u.chars().all(|c| c.is_ascii_alphanumeric() || symbol(c, "email"))
            && d.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            emit(out, end, || format!("username: \"{u}\" domain: \"{d}\""), 1.0);
        }
    }

    // 2. URL
    for (p, tag, w) in [
        ("https://www.", "httpswww", 1.0),
        ("http://www.", "httpwww", 1.0),
        ("https://", "https", 1.01),
        ("http://", "http", 1.01),
        ("www.", "www", 1.02),
    ] {
        if let Some(d) = s.strip_prefix(p) {
            if !d.is_empty() && d.chars().all(|c| alnum(c) || symbol(c, "url")) {
                emit(out, end, || format!("protocol: \"{tag}\" domain: \"{d}\""), w);
            }
        }
    }

    // 3. Paths
    let is_windows = s.len() > 3
        && s.as_bytes()[0].is_ascii_alphabetic()
        && s[1..].starts_with(":\\")
        && s[3..].chars().all(|c| alnum(c) || symbol(c, "windows") || c == ' ');
    let is_abs = s.strip_prefix('/').is_some_and(|r| !r.is_empty() && r.chars().all(|c| alnum(c) || symbol(c, "unix")));
    let is_rel = s.split_once('/').is_some_and(|(a, b)| {
        !a.is_empty()
            && !a.starts_with('$')
            && a.chars().all(|c| alnum(c) || symbol(c, "unix"))
            && b.chars().all(|c| alnum(c) || symbol(c, "unix"))
    });
    let is_back = s.strip_prefix('\\').is_some_and(|r| {
        !r.is_empty() && r.chars().all(|c| alnum(c) || (symbol(c, "unix") && c != '/') || matches!(c, '\\' | ' '))
    });
    if is_windows || is_abs || is_rel || is_back {
        emit(out, end, || format!("path: \"{s}\""), 1.0);
    }

    // 4. IP Address
    let mut parts = s.split('.');
    if let (Some(a), Some(b), Some(c), Some(d), None) =
        (parts.next(), parts.next(), parts.next(), parts.next(), parts.next())
    {
        if [a, b, c, d].iter().all(|p| !p.is_empty() && p.len() <= 3 && p.bytes().all(|c| c.is_ascii_digit())) {
            emit(out, end, || format!("domain: \"{s}\""), 1.0);
        }
    }

    // 5. Combined Domain
    let domain = s.strip_suffix('/').unwrap_or(s);
    let mut parts = domain.split('.');
    let first = parts.next().unwrap_or("");
    let mut count = 1;
    let mut last = first;
    let mut valid = !first.is_empty() && first.chars().all(|c| c.is_ascii_alphanumeric() || c == '-');
    for part in parts {
        count += 1;
        valid &= !part.is_empty() && part.chars().all(|c| c.is_ascii_alphanumeric() || c == '-');
        last = part;
    }
    static DOMAIN_SET: std::sync::LazyLock<std::collections::HashSet<&'static str>> =
        std::sync::LazyLock::new(|| DOMAINS.lines().map(|line| line.trim_end_matches('\r')).collect());
    if count >= 2 && valid && DOMAIN_SET.contains(last) {
        emit(out, end, || format!("domain: \"{s}\""), 1.1);
    }

    // 6. File with Extension
    static EXT_COUNTS: std::sync::LazyLock<std::collections::HashMap<&'static str, usize>> =
        std::sync::LazyLock::new(|| {
            let mut counts = std::collections::HashMap::new();
            for ext in EXT.lines().map(|line| line.trim_end_matches('\r')) {
                *counts.entry(ext).or_insert(0) += 1;
            }
            counts
        });
    if let Some((stem, ext)) = s.rsplit_once('.') {
        if !stem.is_empty() && stem.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '-' | '_')) {
            for _ in 0..EXT_COUNTS.get(ext).copied().unwrap_or(0) {
                emit(out, end, || format!("domain: \"{s}\""), 1.1);
            }
        }
    }

    // 7. Chemical Formula
    if s.chars().any(|c| ('₀'..='₉').contains(&c))
        && s.chars().all(|c| c.is_ascii_alphanumeric() || ('₀'..='₉').contains(&c) || symbol(c, "chem"))
        && !(s.ends_with(')') && !s.contains('('))
    {
        let mut seen = false;
        let mut possible = true;
        for c in s.chars() {
            let mut b = [0; 4];
            let key: &str = c.encode_utf8(&mut b);
            if rows(ELEMENTS).any(|(element, _)| element == key) {
                seen = true;
            } else if symbol(c, "chem") || seen && (c.is_ascii_digit() || table(SUB, key).is_some()) {
            } else {
                possible = false;
                break;
            }
        }
        if possible && seen {
            emit(out, end, || format!("fragment_id: \"{s}\""), 1.2);
        }
    }
}

fn render(s: &str, chem: bool) -> Option<String> {
    let mut result = Vec::new();
    let mut run = String::new();
    for c in s.chars() {
        if c.is_ascii_alphabetic() && !chem {
            run.push(c);
            continue;
        }
        if !run.is_empty() {
            result.push(std::mem::take(&mut run));
        }
        let mut b = [0; 4];
        let key: &str = c.encode_utf8(&mut b);
        let upper = c.to_ascii_uppercase();
        let mut ub = [0; 4];
        let word = if chem && c.is_ascii_alphabetic() {
            table(LETTERS, upper.encode_utf8(&mut ub))
        } else {
            numbers::digit(c).or_else(|| table(SUB, key)).or_else(|| table(SYMBOLS, key))
        }?;
        result.push(word.to_owned());
    }
    if !run.is_empty() {
        result.push(run);
    }
    Some(result.join(" "))
}

pub fn verbalize(d: &Dict) -> Option<String> {
    let mut f = Fields::new(d);
    if let Some(p) = f.opt("protocol")? {
        let protocol = table(PROTOCOLS, p)?;
        let domain = f.take("domain")?;
        if !f.done() {
            return None;
        }
        return Some(format!("{protocol} {}", render(domain, false)?));
    }
    if let Some(u) = f.opt("username")? {
        let d = f.take("domain")?;
        if !f.done() {
            return None;
        }
        return Some(format!("{} ॲट {}", render(u, false)?, render(d, false)?));
    }
    for (key, chem) in [("domain", false), ("path", false), ("fragment_id", true)] {
        if let Some(v) = f.opt(key)? {
            if !f.done() {
                return None;
            }
            return render(v, chem);
        }
    }
    None
}

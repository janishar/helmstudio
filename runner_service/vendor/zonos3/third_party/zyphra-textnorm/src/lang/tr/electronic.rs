//! URL, email and IPv4 branches from `tr/taggers/electronic.py`.
use super::graph_utils::{digit_sequence, is_positive, positive};
use std::sync::LazyLock;
static TLDS: LazyLock<[Vec<&'static str>; 256]> = LazyLock::new(|| {
    let mut index: [Vec<&'static str>; 256] = std::array::from_fn(|_| Vec::new());
    for row in include_str!("../../../data/tr/tld_map.tsv").lines() {
        if let Some((tld, _)) = row.split_once('\t') {
            if let Some(&first) = tld.as_bytes().first() {
                index[first as usize].push(tld);
            }
        }
    }
    index
});
fn domain(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
}
fn valid_path(s: &str) -> bool {
    s.strip_prefix('/').is_some_and(|rest| {
        rest.bytes().all(|c| c.is_ascii_alphanumeric() || matches!(c, b'/' | b'-' | b'_' | b'.' | b'=' | b'?' | b'&'))
    })
}
fn domain_count(s: &str, allow_no_tld: bool, allow_path: bool) -> usize {
    let mut total = 0;
    for (i, _) in s.char_indices().chain(std::iter::once((s.len(), ' '))) {
        if i == 0 {
            continue;
        }
        let previous = s.as_bytes()[i - 1];
        if !(previous.is_ascii_alphanumeric() || matches!(previous, b'-' | b'_')) {
            break;
        }
        let suffix = &s[i..];
        let matching_tlds = suffix.as_bytes().first().map_or(&[][..], |&b| TLDS[b as usize].as_slice());
        for tld in std::iter::once("").chain(matching_tlds.iter().copied()) {
            if tld.is_empty() && !allow_no_tld {
                continue;
            }
            let Some(mut tail) = suffix.strip_prefix(tld) else { continue };
            if let Some(p) = tail.strip_prefix(':') {
                if !allow_no_tld {
                    continue;
                }
                let len = p.bytes().take_while(|c| c.is_ascii_digit()).count();
                if len == 0 || len > 5 || !is_positive(&p[..len]) {
                    continue;
                }
                tail = &p[len..];
            }
            if tail.is_empty() || (allow_path && valid_path(tail)) {
                total += 1;
            }
        }
    }
    total
}
pub fn count(s: &str) -> usize {
    let mut total = 0;
    let mut parts = s.split('.');
    if let (Some(a), Some(b), Some(c), Some(d), None) =
        (parts.next(), parts.next(), parts.next(), parts.next(), parts.next())
    {
        if [a, b, c, d].iter().all(|x| x.len() <= 3 && is_positive(x)) {
            total += 1;
        }
    }
    for row in include_str!("../../../data/tr/proto_map.tsv").lines() {
        if let Some((prefix, _)) = row.split_once('\t') {
            if let Some(rest) = s.strip_prefix(prefix) {
                total += domain_count(rest, true, true);
            }
        }
    }
    total += domain_count(s, false, true);
    if let Some((user, host)) = s.split_once('@') {
        if !user.is_empty() && user.bytes().all(|c| c.is_ascii_alphanumeric() || matches!(c, b'.' | b'_' | b'-' | b'+'))
        {
            total += domain_count(host, false, false);
        }
    }
    total
}
fn path(s: &str) -> Option<String> {
    let rest = s.strip_prefix('/')?;
    let mut out = String::from(" bölü ");
    for c in rest.chars() {
        match c {
            '0'..='9' => out.push_str(&digit_sequence(&c.to_string())?),
            'a'..='z' | 'A'..='Z' => out.push(c),
            '/' => out.push_str(" bölü "),
            '-' => out.push_str(" tire "),
            '_' => out.push_str(" alt çizgi "),
            '.' => out.push_str(" nokta "),
            '=' => out.push_str(" eşittir "),
            '?' => out.push_str(" soru işareti "),
            '&' => out.push_str(" ve "),
            _ => return None,
        }
    }
    Some(out)
}
fn domain_tail(s: &str, allow_no_tld: bool) -> Vec<String> {
    let mut out = Vec::new();
    for (i, _) in s.char_indices().chain(std::iter::once((s.len(), ' '))) {
        let host = &s[..i];
        if !domain(host) {
            continue;
        }
        let suffix = &s[i..];
        let tlds = std::iter::once(("", ""))
            .chain(include_str!("../../../data/tr/tld_map.tsv").lines().filter_map(|row| row.split_once('\t')));
        for (tld, spoken) in tlds {
            if tld.is_empty() && !allow_no_tld {
                continue;
            }
            let Some(mut tail) = suffix.strip_prefix(tld) else { continue };
            let mut value = format!("{host}{spoken}");
            if let Some(p) = tail.strip_prefix(':') {
                if !allow_no_tld {
                    continue;
                }
                let count = p.bytes().take_while(|c| c.is_ascii_digit()).count();
                if count == 0 || count > 5 {
                    continue;
                }
                let Some(n) = positive(&p[..count]) else { continue };
                value.push_str(&format!(" iki nokta {n}"));
                tail = &p[count..];
            }
            if tail.is_empty() {
                out.push(value);
            } else if let Some(p) = path(tail) {
                out.push(format!("{value}{p}"));
            }
        }
    }
    out
}
pub fn readings(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    let chunks: Vec<_> = s.split('.').collect();
    if chunks.len() == 4 && chunks.iter().all(|v| v.len() <= 3) {
        let p: Option<Vec<_>> = chunks.iter().map(|v| positive(v)).collect();
        if let Some(p) = p {
            out.push(p.join(" nokta "));
        }
    }
    for row in include_str!("../../../data/tr/proto_map.tsv").lines() {
        if let Some((a, b)) = row.split_once('\t') {
            if let Some(rest) = s.strip_prefix(a) {
                for d in domain_tail(rest, true) {
                    out.push(format!("{b}{d}"));
                }
            }
        }
    }
    out.extend(domain_tail(s, false));
    if let Some((user, host)) = s.split_once('@') {
        if !user.is_empty() {
            let mut pieces = Vec::new();
            let mut valid = true;
            for c in user.chars() {
                let v = match c {
                    'a'..='z' | 'A'..='Z' => c.to_string(),
                    '0'..='9' => digit_sequence(&c.to_string()).unwrap(),
                    '.' => " nokta ".into(),
                    '_' => " alt çizgi ".into(),
                    '-' => " tire ".into(),
                    '+' => " artı ".into(),
                    _ => {
                        valid = false;
                        break;
                    }
                };
                pieces.push(v);
            }
            if valid {
                for d in domain_tail(host, false) {
                    if !d.contains(" bölü ") && !d.contains(" iki nokta ") {
                        out.push(format!("{} et {d}", pieces.join(" ")));
                    }
                }
            }
        }
    }
    out
}

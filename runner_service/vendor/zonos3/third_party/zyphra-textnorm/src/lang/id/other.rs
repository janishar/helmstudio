use super::number::{cardinal, digits, grouped};
use std::sync::LazyLock;

static DOMAINS: LazyLock<std::collections::HashSet<&'static str>> = LazyLock::new(|| {
    include_str!("../../../data/id/electronic/domain.tsv")
        .lines()
        .filter_map(|l| l.split_once('\t').map(|(key, _)| key))
        .collect()
});

fn name(s: &str) -> String {
    if super::lazy::recognizing() { String::new() } else { format!("name: \"{}\"", s.replace(' ', "\u{a0}")) }
}

struct WhitelistIndex {
    values: std::collections::HashMap<&'static str, &'static str>,
    first: [bool; 256],
    lengths: Vec<bool>,
}

pub(crate) fn whitelist(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    static WHITELIST: LazyLock<WhitelistIndex> = LazyLock::new(|| {
        let values: std::collections::HashMap<_, _> = include_str!("../../../data/id/whitelist.tsv")
            .lines()
            .filter_map(|l| l.split_once('\t'))
            .map(|(k, v)| (k, v.trim_end_matches('\r')))
            .collect();
        let mut first = [false; 256];
        let mut lengths = vec![false; values.keys().map(|key| key.len()).max().unwrap_or(0) + 1];
        for key in values.keys() {
            if let Some(&byte) = key.as_bytes().first() {
                first[byte as usize] = true;
            }
            lengths[key.len()] = true;
        }
        WhitelistIndex { values, first, lengths }
    });
    if (s.is_empty() || WHITELIST.first[s.as_bytes()[0] as usize]) && WHITELIST.lengths.get(s.len()) == Some(&true) {
        if let Some(&value) = WHITELIST.values.get(s) {
            out.push(name(value));
        }
    }
    if !matches!(s.chars().next(), Some('N' | 'n' | '§' | '©')) {
        return out;
    }
    for (prefix, word) in [
        ("No.", "nomor"),
        ("no.", "nomor"),
        ("N°", "nomor"),
        ("n°", "nomor"),
        ("Nº", "nomor"),
        ("nº", "nomor"),
        ("§", "pasal"),
        ("©", "hak cipta"),
    ] {
        if let Some(rest) = s.strip_prefix(prefix) {
            let rest = rest.trim_start_matches([' ', '\u{a0}', '\u{202f}']);
            if let Some(num) = grouped(rest) {
                out.push(name(&format!("{word} {num}")));
            }
        }
    }
    out
}

fn roman_num(s: &str) -> Option<u32> {
    let values = [(100, "C"), (90, "XC"), (50, "L"), (40, "XL"), (10, "X"), (9, "IX"), (5, "V"), (4, "IV"), (1, "I")];
    let mut input = s;
    let mut n = 0;
    for (v, r) in values {
        while let Some(rest) = input.strip_prefix(r) {
            n += v;
            input = rest;
        }
    }
    if !input.is_empty() || !(1..=100).contains(&n) {
        return None;
    }
    let mut rem = n;
    let mut canonical = String::new();
    for (v, r) in values {
        while rem >= v {
            canonical.push_str(r);
            rem -= v;
        }
    }
    if canonical == s { Some(n) } else { None }
}

const KEY_WORDS: &[&str] = &[
    "bab",
    "abad",
    "bagian",
    "jilid",
    "volume",
    "pasal",
    "ayat",
    "lampiran",
    "buku",
    "seri",
    "tahap",
    "fase",
    "tingkat",
    "kelas",
    "golongan",
    "eselon",
    "semester",
    "triwulan",
    "kuartal",
    "sila",
    "amendemen",
    "amandemen",
    "perang dunia",
];

const MONARCHS: &[&str] = &[
    "Paus",
    "Raja",
    "Ratu",
    "Sultan",
    "Sri Sultan",
    "Kaisar",
    "Hamengkubuwono",
    "Hamengkubuwana",
    "Pakubuwono",
    "Pakubuwana",
    "Mangkunegara",
    "Pakualam",
    "Paku Alam",
    "Yohanes",
    "Yohanes Paulus",
    "Benediktus",
    "Fransiskus",
    "Pius",
    "Leo",
    "Paulus",
    "Gregorius",
    "Elizabeth",
    "Charles",
    "Henry",
    "George",
    "Edward",
    "William",
    "Richard",
    "James",
    "Victoria",
    "Louis",
    "Napoleon",
    "Philip",
    "Felipe",
    "Wilhelm",
    "Nicholas",
    "Nikolai",
    "Alexander",
    "Aleksander",
    "Rama",
    "Hassan",
    "Mohammed",
    "Abdullah",
    "Salman",
    "Ramses",
];

pub(crate) fn roman(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some((kw, rom)) = s.rsplit_once(' ') {
        let kw_lower = kw.to_ascii_lowercase();
        if KEY_WORDS.contains(&kw_lower.as_str()) {
            if let Some(r) = rom.strip_prefix("ke-").or_else(|| rom.strip_prefix("Ke-")) {
                if !["C", "D", "L", "M"].contains(&r) {
                    if let Some(n) = roman_num(r) {
                        let ord = if n == 1 {
                            "pertama".to_string()
                        } else {
                            format!("ke{}", cardinal(&n.to_string()).unwrap())
                        };
                        out.push(name(&format!("{kw} {ord}")));
                        return out;
                    }
                }
            }
            if !["C", "D", "L", "M"].contains(&rom) {
                if let Some(n) = roman_num(rom) {
                    if let Some(card) = cardinal(&n.to_string()) {
                        out.push(name(&format!("{kw} {card}")));
                        return out;
                    }
                }
            }
        }
        if MONARCHS.contains(&kw) {
            if let Some(n) = roman_num(rom) {
                if (1..=39).contains(&n) {
                    if let Some(card) = cardinal(&n.to_string()) {
                        out.push(name(&format!("{kw} {card}")));
                        return out;
                    }
                }
            }
        }
    }
    if let Some(r) = s.strip_prefix("ke-").or_else(|| s.strip_prefix("Ke-")) {
        if !["C", "D", "L", "M"].contains(&r) {
            if let Some(n) = roman_num(r) {
                let ord =
                    if n == 1 { "pertama".to_string() } else { format!("ke{}", cardinal(&n.to_string()).unwrap()) };
                out.push(name(&ord));
            }
        }
    }
    out
}

pub(crate) fn telephone(s: &str) -> Option<String> {
    fn groups(s: &str) -> Option<usize> {
        let mut need_digit = true;
        let mut count = 0;
        for c in s.bytes() {
            if c.is_ascii_digit() {
                count += 1;
                need_digit = false;
            } else if matches!(c, b' ' | b'-' | b'.') && !need_digit {
                need_digit = true;
            } else {
                return None;
            }
        }
        (!need_digit).then_some(count)
    }

    let (country, local) = if let Some(rest) = s.strip_prefix('+') {
        let end = rest.bytes().take_while(u8::is_ascii_digit).count();
        if !(1..=3).contains(&end) || !matches!(rest.as_bytes().get(end), Some(b' ' | b'-')) {
            return None;
        }
        (Some(&rest[..end]), &rest[end + 1..])
    } else {
        if !s.starts_with('0') && !s.starts_with("(0") {
            return None;
        }
        for sep in ["/", "-", "."] {
            let mut parts = s.split(sep);
            if let (Some(a), Some(b), Some(c), None) = (parts.next(), parts.next(), parts.next(), parts.next()) {
                if a.len() == 2
                    && b.len() == 2
                    && c.len() == 4
                    && a.bytes().chain(b.bytes()).chain(c.bytes()).all(|c| c.is_ascii_digit())
                {
                    return None;
                }
            }
        }
        (None, s)
    };
    let (area, body) = if let Some(rest) = local.strip_prefix('(') {
        let (area, body) = rest.split_once(')')?;
        if area.is_empty() || !area.bytes().all(|c| c.is_ascii_digit()) {
            return None;
        }
        (area, body.strip_prefix(' ').unwrap_or(body))
    } else {
        ("", local)
    };
    if area.len() + groups(body)? < 7 {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let mut raw = String::with_capacity(area.len() + body.len());
    raw.push_str(area);
    raw.extend(body.chars().filter(char::is_ascii_digit));
    let spoken = digits(&raw)?;
    Some(if let Some(country) = country {
        let country = digits(country)?;
        format!("telephone {{ country_code: \"plus {country}\" number_part: \"{spoken}\" preserve_order: true }}")
    } else {
        format!("telephone {{ number_part: \"{spoken}\" preserve_order: true }}")
    })
}

fn label(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
}

fn host(s: &str, require_dot: bool) -> bool {
    if require_dot && !s.contains('.') {
        return false;
    }
    let (head, path) = s.split_once('/').unwrap_or((s, ""));
    if !path.is_empty() {
        for segment in path.split('/') {
            if !segment.is_empty() && !segment.chars().all(|c| c.is_ascii_alphanumeric() || "-_.?=&+~#%".contains(c)) {
                return false;
            }
        }
    }
    let (host, port) = head.split_once(':').unwrap_or((head, ""));
    if !port.is_empty() && (!port.bytes().all(|b| b.is_ascii_digit()) || !(1..=5).contains(&port.len())) {
        return false;
    }
    let mut parts = host.split('.');
    let Some(first) = parts.next() else { return false };
    if !label(first) {
        return false;
    }
    let mut count = 1;
    for part in parts {
        if !label(part) {
            return false;
        }
        count += 1;
    }
    !require_dot || count >= 2
}

pub(crate) fn electronic(s: &str) -> Option<String> {
    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty()
            && user.chars().all(|c| c.is_ascii_alphanumeric() || "-_.+~".contains(c))
            && host(domain, true)
        {
            return Some(if super::lazy::recognizing() {
                String::new()
            } else {
                format!("electronic {{ username: \"{user}\" domain: \"{domain}\" preserve_order: true }}")
            });
        }
    }
    for proto in ["https://www.", "http://www.", "https://", "http://", "www."] {
        if let Some(domain) = s.strip_prefix(proto) {
            if host(domain, false) {
                return Some(if super::lazy::recognizing() {
                    String::new()
                } else {
                    format!("electronic {{ protocol: \"{proto}\" domain: \"{domain}\" preserve_order: true }}")
                });
            }
        }
    }
    let (head, _) = s.split_once('/').unwrap_or((s, ""));
    let (host_str, _) = head.split_once(':').unwrap_or((head, ""));
    let mut parts: Vec<&str> = host_str.split('.').collect();
    if parts.len() >= 2 && parts.iter().all(|p| label(p)) {
        let tld = parts.pop().unwrap();
        let dot_tld = format!(".{tld}");
        if DOMAINS.contains(dot_tld.as_str()) && host(s, true) {
            return Some(if super::lazy::recognizing() {
                String::new()
            } else {
                format!("electronic {{ domain: \"{s}\" preserve_order: true }}")
            });
        }
    }
    None
}

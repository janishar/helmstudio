use super::{
    lookup,
    number::{cardinal, digits, grouped},
};
static DOMAINS: std::sync::LazyLock<std::collections::HashSet<&'static str>> = std::sync::LazyLock::new(|| {
    include_str!("../../../data/ms/electronic/domain.tsv")
        .lines()
        .filter_map(|l| l.split_once('\t').map(|(key, _)| key))
        .collect()
});
fn name(s: &str) -> String {
    if super::lazy::recognizing() { String::new() } else { format!("name: \"{}\"", s.replace(' ', "\u{a0}")) }
}
pub(crate) fn whitelist(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    static MAX_KEY: std::sync::LazyLock<usize> = std::sync::LazyLock::new(|| {
        include_str!("../../../data/ms/whitelist.tsv")
            .lines()
            .filter_map(|l| l.split_once('\t').map(|(key, _)| key.len()))
            .max()
            .unwrap_or(0)
    });
    if s.len() <= *MAX_KEY {
        if let Some(v) = lookup(include_str!("../../../data/ms/whitelist.tsv"), s) {
            out.push(name(v));
        }
    }
    for (p, w) in [
        ("No.", "nombor"),
        ("no.", "nombor"),
        ("No", "nombor"),
        ("no", "nombor"),
        ("N°", "nombor"),
        ("n°", "nombor"),
        ("Nº", "nombor"),
        ("nº", "nombor"),
        ("#", "nombor"),
        ("§", "seksyen"),
        ("©", "hak cipta"),
    ] {
        if let Some(r) = s.strip_prefix(p) {
            let r = r.strip_prefix([' ', '\u{a0}', '\u{202f}']).unwrap_or(r);
            if let Some(n) = grouped(r) {
                out.push(if super::lazy::recognizing() { String::new() } else { name(&format!("{w} {n}")) });
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
        while let Some(x) = input.strip_prefix(r) {
            input = x;
            n += v;
        }
    }
    if !input.is_empty() || !(1..=100).contains(&n) {
        return None;
    }
    let mut rem = n;
    let mut canonical = String::new();
    for (v, r) in values {
        while rem >= v {
            rem -= v;
            canonical.push_str(r);
        }
    }
    if canonical == s { Some(n) } else { None }
}
pub(crate) fn roman(s: &str) -> Vec<String> {
    const WORDS: &[&str] = &[
        "bab",
        "abad",
        "bahagian",
        "jilid",
        "volum",
        "volume",
        "seksyen",
        "fasal",
        "pasal",
        "ayat",
        "lampiran",
        "buku",
        "siri",
        "peringkat",
        "tahap",
        "fasa",
        "kelas",
        "semester",
        "suku tahun",
        "perang dunia",
    ];
    const NAMES: &[&str] = &[
        "Sultan",
        "Raja",
        "Permaisuri",
        "Yang di-Pertuan Agong",
        "Paus",
        "Kaisar",
        "Maharaja",
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
        "Hamengkubuwono",
        "Pakubuwono",
        "Mangkunegara",
        "Yohanes",
        "Benediktus",
        "Fransiskus",
        "Pius",
        "Leo",
        "Paulus",
        "Gregorius",
    ];
    let mut out = Vec::new();
    let (key, r) = s.rsplit_once(' ').unwrap_or(("", s));
    let (r, ord) =
        if let Some(x) = r.strip_prefix("ke-").or_else(|| r.strip_prefix("Ke-")) { (x, true) } else { (r, false) };
    if let Some(n) = roman_num(r) {
        let safe = !matches!(r, "C" | "D" | "L" | "M");
        let structural = WORDS.iter().any(|w| {
            let title = w
                .split(' ')
                .map(|part| {
                    let mut cs = part.chars();
                    match cs.next() {
                        Some(c) => format!("{}{}", c.to_uppercase().collect::<String>(), cs.as_str()),
                        None => String::new(),
                    }
                })
                .collect::<Vec<_>>()
                .join(" ");
            let capital = format!("{}{}", &w[..1].to_uppercase(), &w[1..]);
            key == *w || key == title || key == capital || key == w.to_uppercase()
        });
        let monarch = NAMES.contains(&key) && n < 40;
        if (ord && safe && (key.is_empty() || structural)) || (!ord && ((structural && safe) || monarch)) {
            if let Some(w) = cardinal(&n.to_string()) {
                let w = if ord { if n == 1 { "pertama".to_owned() } else { format!("ke{w}") } } else { w };
                out.push(name(&if key.is_empty() { w } else { format!("{key} {w}") }));
            }
        }
    }
    out
}
pub(crate) fn telephone(s: &str) -> Option<String> {
    let (country, local) = if let Some(r) = s.strip_prefix('+') {
        let i = r.find([' ', '-'])?;
        if !(1..=3).contains(&i) {
            return None;
        }
        let spoken = digits(&r[..i])?;
        (Some(if super::lazy::recognizing() { String::new() } else { format!("plus {spoken}") }), &r[i + 1..])
    } else {
        (None, s)
    };
    if country.is_none() {
        for sep in ['-', '.'] {
            let mut parts = local.split(sep);
            let a = parts.next().unwrap_or("");
            let b = parts.next().unwrap_or("");
            let c = parts.next().unwrap_or("");
            if a.len() == 2
                && b.len() == 2
                && c.len() == 4
                && parts.next().is_none()
                && [a, b, c].iter().all(|p| p.bytes().all(|b| b.is_ascii_digit()))
            {
                return None;
            }
        }
    }
    let mut body = local;
    let mut parts = Vec::new();
    if let Some(r) = body.strip_prefix('(') {
        let (area, rest) = r.split_once(')')?;
        let spoken = digits(area)?;
        if !super::lazy::recognizing() {
            parts.push(spoken);
        }
        body = rest.strip_prefix(' ').unwrap_or(rest);
    }
    if country.is_none() && !local.trim_start_matches('(').starts_with('0') {
        return None;
    }
    if !body.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.')) {
        return None;
    }
    for g in body.split([' ', '-', '.']) {
        let spoken = digits(g)?;
        if !super::lazy::recognizing() {
            parts.push(spoken);
        }
    }
    if local.chars().filter(|c| c.is_ascii_digit()).count() < 7 {
        return None;
    }
    if super::lazy::recognizing() {
        return Some(String::new());
    }
    let cc = country.map(|v| format!("country_code: \"{v}\" ")).unwrap_or_default();
    Some(format!("telephone {{ {cc}number_part: \"{}\" preserve_order: true }}", parts.join(" ")))
}
fn label(s: &str) -> bool {
    !s.is_empty() && s.bytes().all(|c| c.is_ascii_alphanumeric() || c == b'-' || c == b'_')
}
fn host(s: &str, require_dot: bool) -> bool {
    let (authority, path) = s.split_once('/').map(|(a, b)| (a, Some(b))).unwrap_or((s, None));
    let (host, port) = authority.split_once(':').map(|(a, b)| (a, Some(b))).unwrap_or((authority, None));
    if let Some(port) = port {
        if port.is_empty() || port.len() > 5 || !port.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
    }
    if let Some(path) = path {
        for segment in path.split('/') {
            if !segment.is_empty() {
                if !segment.chars().all(|c| c.is_ascii_alphanumeric() || ".-_:~#+=?&%".contains(c)) {
                    return false;
                }
                if !segment.chars().last().map(|c| c.is_ascii_alphanumeric() || matches!(c, '=' | '_')).unwrap_or(false)
                {
                    return false;
                }
            }
        }
    }
    (!require_dot || host.contains('.')) && host.split('.').all(label)
}
pub(crate) fn electronic(s: &str) -> Option<String> {
    let (proto, rest) = if let Some(r) = s.strip_prefix("https://www.") {
        (Some("https://www."), r)
    } else if let Some(r) = s.strip_prefix("http://www.") {
        (Some("http://www."), r)
    } else if let Some(r) = s.strip_prefix("https://") {
        (Some("https://"), r)
    } else if let Some(r) = s.strip_prefix("http://") {
        (Some("http://"), r)
    } else if let Some(r) = s.strip_prefix("www.") {
        (Some("www."), r)
    } else {
        (None, s)
    };
    if let Some(p) = proto {
        if host(rest, false) {
            return Some(if super::lazy::recognizing() {
                String::new()
            } else {
                format!("electronic {{ protocol: \"{p}\" domain: \"{rest}\" preserve_order: true }}")
            });
        }
        return None;
    }
    if let Some((u, d)) = rest.split_once('@') {
        if !u.is_empty()
            && u.chars().all(|c| c.is_ascii_alphanumeric() || ".-_/ :~#+=?&%".contains(c) && c != ' ')
            && host(d, true)
        {
            return Some(if super::lazy::recognizing() {
                String::new()
            } else {
                format!("electronic {{ username: \"{u}\" domain: \"{d}\" preserve_order: true }}")
            });
        }
        return None;
    }
    let hostname = rest.split('/').next().unwrap_or("").split(':').next().unwrap_or("");
    if host(rest, true) && hostname.rfind('.').is_some_and(|i| DOMAINS.contains(&hostname[i..])) {
        return Some(if super::lazy::recognizing() {
            String::new()
        } else {
            format!("electronic {{ domain: \"{rest}\" preserve_order: true }}")
        });
    }
    None
}

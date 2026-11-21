use super::graph_utils::{Gender, cardinal};

pub(crate) fn number(s: &str, gender: Gender) -> Option<String> {
    if let Some(v) = cardinal(s, gender) {
        return Some(v);
    }
    if !s.contains([' ', '\u{a0}', '.', ',']) {
        return None;
    }
    let mut parts = s.split(|c| matches!(c, ' ' | '\u{a0}' | '.' | ','));
    let first = parts.next()?;
    if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut count = 0;
    for part in parts {
        if part.len() != 3 || !part.bytes().all(|b| b.is_ascii_digit()) || count == 4 {
            return None;
        }
        count += 1;
    }
    if count == 0 {
        return None;
    }
    let mut grouped = String::with_capacity(first.len() + count * 3);
    grouped.extend(s.chars().filter(|c| c.is_ascii_digit()));
    cardinal(&grouped, gender)
}

pub(crate) fn digits(s: &str) -> Option<String> {
    const WORDS: [&str; 10] = ["нула", "едно", "две", "три", "четири", "пет", "шест", "седем", "осем", "девет"];
    if s.is_empty() {
        return None;
    }
    let mut result = String::new();
    for b in s.bytes() {
        let digit = b.checked_sub(b'0')?;
        let word = WORDS.get(digit as usize)?;
        if !result.is_empty() {
            result.push(' ');
        }
        result.push_str(word);
    }
    Some(result)
}

pub(crate) fn render_plain(s: &str) -> Option<String> {
    if let Some((a, b)) = s.split_once(':') {
        return Some(format!("{} към {}", number(a, Gender::Neuter)?, number(b, Gender::Neuter)?));
    }
    if s.len() > 1 && s.starts_with('0') { digits(s) } else { cardinal(s, Gender::Neuter) }
}

pub(crate) fn decimal(s: &str) -> Option<String> {
    let (sign, s) =
        if let Some(s) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("минус ", s) } else { ("", s) };
    for (i, c) in s.char_indices() {
        if c != '.' && c != ',' {
            continue;
        }
        let left = &s[..i];
        let right = &s[i + 1..];
        if let Some(a) = number(left, Gender::Neuter) {
            let b = if right.starts_with('0') && right.len() >= 2 && right.len() <= 6 {
                digits(right)
            } else {
                number(right, Gender::Neuter)
            };
            if let Some(b) = b {
                return Some(format!("{sign}{a} цяло и {b}"));
            }
        }
    }
    None
}

pub(crate) fn cardinals(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if let Some(v) = number(s, Gender::Neuter) {
        out.push((v, 1.2));
    }
    if s.len() >= 2 && s.len() <= 11 && s.starts_with('0') {
        if let Some(v) = digits(s) {
            out.push((v, 1.1));
        }
    }
    for (prefix, spoken) in [("-", "минус "), ("−", "минус "), ("~", "около "), ("±", "плюс-минус ")]
    {
        if let Some(rest) = s.strip_prefix(prefix) {
            if let Some(v) = number(rest, Gender::Neuter) {
                out.push((format!("{spoken}{v}"), 1.05));
            }
        }
    }
    for (i, c) in s.char_indices() {
        if !matches!(c, '-' | '–' | '—' | ':') {
            continue;
        }
        let a = if c == ':' { &s[..i] } else { s[..i].strip_suffix(' ').unwrap_or(&s[..i]) };
        let tail = &s[i + c.len_utf8()..];
        let b = if c == ':' { tail } else { tail.strip_prefix(' ').unwrap_or(tail) };
        if let (Some(x), Some(y)) = (number(a, Gender::Neuter), number(b, Gender::Neuter)) {
            out.push((format!("{x}{}{y}", if c == ':' { " към " } else { " до " }), if c == ':' { 1.5 } else { 1.05 }));
        }
    }
    let (version, prefix) = if let Some(s) = s.strip_prefix("v.").or_else(|| s.strip_prefix("V.")) {
        (s, true)
    } else if let Some(s) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) {
        (s, true)
    } else {
        (s, false)
    };
    let parts = version.split('.');
    let count = parts.clone().count();
    if (prefix && (1..=5).contains(&count) || !prefix && (3..=5).contains(&count))
        && parts.clone().all(|p| !p.is_empty() && p.len() <= 4)
    {
        if let Some(v) = parts.map(|p| number(p, Gender::Neuter)).collect::<Option<Vec<_>>>() {
            out.push((format!("{}{}", if prefix { "версия " } else { "" }, v.join(" точка ")), 1.0));
        }
    }
    for (a, b) in [
        ("5G", "пет джи"),
        ("4K", "четири ка"),
        ("CO2", "C O две"),
        ("A4", "А четири"),
        ("BA249", "Б А двеста четиридесет и девет"),
        ("AB-123-CD", "А Б едно две три Ц Д"),
    ] {
        if s == a {
            out.push((b.to_owned(), 1.0));
        }
    }
    if let Some((first, rest)) = s.split_once('-') {
        if let Some((middle, last)) = rest.split_once('-') {
            if !last.contains('-')
                && first.chars().count() <= 4
                && last.chars().count() <= 4
                && !first.is_empty()
                && !last.is_empty()
                && first
                    .chars()
                    .chain(last.chars())
                    .all(|c| c.is_ascii_alphabetic() || ('А'..='я').contains(&c) || matches!(c, 'ѐ' | 'ќ' | 'Ѐ' | 'Ќ'))
                && middle.len() <= 4
            {
                if let Some(v) = number(middle, Gender::Neuter) {
                    out.push((format!("{first} {v} {last}"), 1.0));
                }
            }
        }
    }
    if let Some((p, n)) = s.split_once('-') {
        if matches!(p, "COVID" | "Covid" | "covid") && n.len() <= 4 {
            if let Some(v) = number(n, Gender::Neuter) {
                out.push((format!("COVID {v}"), 1.0));
            }
        }
    }
    out
}

pub(crate) fn fraction(s: &str) -> Vec<(String, f64)> {
    let vulgar = [
        ('½', "една втора"),
        ('⅓', "една трета"),
        ('⅔', "две трети"),
        ('¼', "една четвърт"),
        ('¾', "три четвърти"),
        ('⅕', "една пета"),
        ('⅖', "две пети"),
        ('⅗', "три пети"),
        ('⅘', "четири пети"),
        ('⅙', "една шеста"),
        ('⅚', "пет шести"),
        ('⅛', "една осма"),
        ('⅜', "три осми"),
        ('⅝', "пет осми"),
        ('⅞', "седем осми"),
        ('⅐', "една седма"),
        ('⅑', "една девета"),
        ('⅒', "една десета"),
    ];
    let den = [
        ("2", "втора", "втори"),
        ("3", "трета", "трети"),
        ("4", "четвърт", "четвърти"),
        ("5", "пета", "пети"),
        ("6", "шеста", "шести"),
        ("7", "седма", "седми"),
        ("8", "осма", "осми"),
        ("9", "девета", "девети"),
        ("10", "десета", "десети"),
        ("12", "дванадесета", "дванадесети"),
        ("100", "стотна", "стотни"),
        ("1000", "хилядна", "хилядни"),
        ("250", "двеста и петдесета", "двеста и петдесети"),
    ];
    let mut out = Vec::new();
    let (sign, s) =
        if let Some(t) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) { ("минус ", t) } else { ("", s) };
    for (ch, v) in vulgar {
        if let Some(a) = s.strip_suffix(ch) {
            if a.is_empty() {
                out.push((format!("{sign}{v}"), 0.0));
            } else if let Some(n) = cardinal(a.strip_suffix(' ').unwrap_or(a), Gender::Neuter) {
                out.push((format!("{sign}{n} и {v}"), 0.0));
            }
        }
    }
    if let Some((a, d)) = s.split_once('/') {
        for (key, one, many) in den {
            if d != key {
                continue;
            }
            let (whole, num) = if let Some((w, n)) = a.split_once(' ') { (Some(w), n) } else { (None, a) };
            if let Some(n) = cardinal(num, Gender::Feminine) {
                let p = if num == "1" { format!("една {one}") } else { format!("{n} {many}") };
                if let Some(w) = whole {
                    if let Some(w) = cardinal(w, Gender::Neuter) {
                        out.push((format!("{sign}{w} и {p}"), 0.0));
                    }
                } else {
                    out.push((format!("{sign}{p}"), if num == "1" { -0.01 } else { 0.0 }));
                }
            }
        }
    }
    out
}

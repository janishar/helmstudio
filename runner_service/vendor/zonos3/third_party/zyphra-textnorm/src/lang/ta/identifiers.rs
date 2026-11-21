//! Telephone, electronic, Roman and serial identifiers.
use super::number::{cardinal, digit, digitwise};
use std::collections::HashSet;
use std::sync::LazyLock;

static TLDS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| include_str!("../../../data/ta/electronic/domain.tsv").lines().collect());
static EXTENSIONS: LazyLock<HashSet<&'static str>> =
    LazyLock::new(|| include_str!("../../../data/ta/electronic/file_extensions.tsv").lines().collect());
fn f(k: &str, v: &str) -> String {
    format!("{k}: \"{v}\"")
}
fn lookup<'a>(table: &'a str, key: &str) -> Option<&'a str> {
    table.lines().filter_map(|l| l.split_once('\t')).find_map(|(k, v)| (k == key).then_some(v))
}
fn letters(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    s.chars()
        .map(|c| {
            let k = c.to_ascii_lowercase().to_string();
            lookup(include_str!("../../../data/ta/address/letters.tsv"), &k).map(str::to_owned)
        })
        .collect::<Option<Vec<_>>>()
        .map(|v| v.join(" "))
}
pub fn international_name(s: &str) -> Option<String> {
    let rest = s.strip_prefix('+')?;
    if !rest.chars().all(|c| c.is_ascii_digit() || " - .()".contains(c)) {
        return None;
    }
    let ds: String = rest.chars().filter(|c| c.is_ascii_digit()).collect();
    if !(7..=15).contains(&ds.len()) {
        return None;
    }
    Some(format!("பிளஸ் {}", digitwise(&ds)?))
}
fn context_paths(s: &str, mut emit: impl FnMut(&str, &str, &str, f64)) {
    for (table, min, weight) in [
        (include_str!("../../../data/ta/telephone/credit_context.tsv"), 4, 0.9),
        (include_str!("../../../data/ta/telephone/pincode_context.tsv"), 6, 1.0),
    ] {
        let mut offset = 0;
        for word in s.split(' ') {
            let start = offset;
            let end = start + word.len();
            offset = end + 1;
            let count = word.chars().count();
            if count < min || count > 19 || !word.chars().all(|c| digit(c).is_some()) {
                continue;
            }
            let before = if start == 0 { "" } else { &s[..start - 1] };
            let after = if end == s.len() { "" } else { &s[end + 1..] };
            for key in table.lines() {
                let leading = if before.is_empty() || before == key {
                    Some("")
                } else {
                    before.strip_prefix(key).and_then(|x| x.strip_prefix(' '))
                };
                let trailing = if after.is_empty() || after == key {
                    Some("")
                } else {
                    after.strip_suffix(key).and_then(|x| x.strip_suffix(' '))
                };
                let valid = |value: &str| {
                    value.is_empty()
                        || (value.split(' ').count() <= 5
                            && value.split(' ').all(|part| {
                                !part.is_empty() && part.chars().all(|c| digit(c).is_none() && !c.is_whitespace())
                            }))
                };
                if leading.is_some_and(valid) && trailing.is_some_and(valid) {
                    emit(before, word, after, weight);
                }
            }
        }
    }
}

pub fn telephone_context_costs(s: &str, mut emit: impl FnMut(f64)) {
    context_paths(s, |_, _, _, weight| emit(weight));
}

pub fn telephone_context(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    context_paths(s, |before, word, after, weight| {
        let mut value = String::new();
        if !before.is_empty() {
            value.push_str(before);
            value.push(' ');
        }
        value.push_str(&digitwise(word).unwrap());
        value.push(' ');
        if !after.is_empty() {
            value.push_str(after);
        }
        out.push((f("number_part", &value), weight));
    });
    out
}
fn grouped(s: &str) -> bool {
    let mut digit_before = false;
    let mut last_digit = false;
    for c in s.chars() {
        if digit(c).is_some() {
            digit_before = true;
            last_digit = true
        } else if matches!(c, ' ' | '-' | '.') && last_digit {
            last_digit = false
        } else {
            return false;
        }
    }
    digit_before && last_digit
}
pub fn telephone(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let ds: String = s.chars().filter(|c| digit(*c).is_some()).collect();
    let n = ds.chars().count();
    let first = ds.chars().next().and_then(digit);
    let mobile_head = if first == Some(0) {
        n == 11 && ds.chars().nth(1).and_then(digit).is_some_and(|x| x >= 6)
    } else {
        n == 10 && first.is_some_and(|x| x >= 6)
    };
    let groups: Vec<_> = s.split(|c| c == ' ' || c == '-').collect();
    let mobile_shape = (groups.len() == 1 && groups[0].chars().all(|c| digit(c).is_some()))
        || (first != Some(0) && groups.len() == 2 && groups[0].chars().count() == 5 && groups[1].chars().count() == 5)
        || (first != Some(0)
            && groups.len() == 3
            && groups[0].chars().count() == 3
            && groups[1].chars().count() == 3
            && groups[2].chars().count() == 4);
    if mobile_head && mobile_shape && groups.iter().all(|g| g.chars().all(|c| digit(c).is_some())) {
        out.push((f("number_part", &format!("{} ", digitwise(&ds).unwrap())), -0.2));
    }
    let plain_landline = first == Some(0)
        && (10..=12).contains(&n)
        && s.chars().all(|c| digit(c).is_some() || matches!(c, ' ' | '-' | '.'))
        && s.chars().filter(|c| matches!(*c, ' ' | '-' | '.')).count() <= 1;
    let bracket_landline = s.strip_prefix('(').and_then(|x| x.split_once(')')).is_some_and(|(std, subscriber)| {
        let subscriber = subscriber.strip_prefix(' ').unwrap_or(subscriber);
        (3..=6).contains(&std.chars().count())
            && std.chars().next().and_then(digit) == Some(0)
            && std.chars().all(|c| digit(c).is_some())
            && (6..=8).contains(&subscriber.chars().count())
            && subscriber.chars().all(|c| digit(c).is_some())
    });
    if plain_landline || bracket_landline {
        out.push((f("number_part", &format!("{} ", digitwise(&ds).unwrap())), 0.8));
    }
    if let Some(rest) = s.strip_prefix('+') {
        for split in 1..=3 {
            let mut chars = rest.char_indices();
            let end = chars.nth(split).map(|x| x.0).unwrap_or(rest.len());
            let (cc, tail) = rest.split_at(end);
            if cc.chars().count() != split || !cc.chars().all(|c| digit(c).is_some()) {
                continue;
            }
            let tail = tail.strip_prefix(' ').or_else(|| tail.strip_prefix('-')).unwrap_or(tail);
            let d: String = tail.chars().filter(|c| digit(*c).is_some()).collect();
            if (6..=12).contains(&d.chars().count()) && grouped(tail) {
                out.push((
                    format!(
                        "{} {}",
                        f("country_code", &format!("பிளஸ் {} ", digitwise(cc).unwrap())),
                        f("number_part", &format!("{} ", digitwise(&d).unwrap()))
                    ),
                    0.7,
                ));
            }
        }
    }
    out
}
pub(super) const ROMAN_KEYS: [&str; 19] = [
    "அத்தியாயம்",
    "பாகம்",
    "பிரிவு",
    "நூற்றாண்டு",
    "ஆம் நூற்றாண்டு",
    "உலகப்போர்",
    "ஆம் உலகப்போர்",
    "வகுப்பு",
    "படிவம்",
    "போப்",
    "ராஜா",
    "அரசர்",
    "சந்திரயான்",
    "அப்பல்லோ",
    "சார்லஸ்",
    "ஜார்ஜ்",
    "எலிசபெத்",
    "ஹென்றி",
    "லூயிஸ்",
];
pub fn roman(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if !s.chars().any(|c| "IVXLCDM".contains(c)) || !s.chars().any(|c| ('\u{b80}'..='\u{bff}').contains(&c)) {
        return out;
    }
    let keys = ROMAN_KEYS;
    let table = include_str!("../../../data/ta/roman/roman_to_spoken.tsv");
    for k in keys {
        for sep in [" ", "-"] {
            if let Some(r) = s.strip_prefix(k).and_then(|rest| rest.strip_prefix(sep)) {
                if lookup(table, r).is_some() {
                    out.push(format!(
                        "preserve_order: true {} {}",
                        f("key_cardinal", &k.replace(' ', "\u{a0}")),
                        f("integer", r)
                    ));
                }
            }
            if let Some(r) = s.strip_suffix(k).and_then(|rest| rest.strip_suffix(sep)) {
                if lookup(table, r).is_some() {
                    out.push(format!(
                        "preserve_order: true {} {}",
                        f("integer", r),
                        f("key_cardinal", &k.replace(' ', "\u{a0}"))
                    ));
                }
            }
        }
    }
    let (base, key) = s.split_once(' ').map(|(a, b)| (a, Some(b))).unwrap_or((s, None));
    for suf in ["-ஆவது", "-வது", "ஆவது", "வது"] {
        if let Some(r) = base.strip_suffix(suf) {
            if let Some(v) = lookup(table, r) {
                if key.is_none() || key.is_some_and(|x| keys.contains(&x)) {
                    let mut t = format!(
                        "preserve_order: true {} {}",
                        f("integer", r),
                        f("default_ordinal", &format!("{v}ாவது"))
                    );
                    if let Some(k) = key {
                        t.push(' ');
                        t.push_str(&f("key_cardinal", k));
                    }
                    out.push(t)
                }
            }
        }
    }
    out
}
fn chemical(s: &str) -> bool {
    if !s.chars().any(|c| ('₀'..='₉').contains(&c)) {
        return false;
    }
    let mut pos = 0;
    let mut seen_element = false;
    let mut open = false;
    while pos < s.len() {
        let rest = &s[pos..];
        let c = rest.chars().next().unwrap();
        if ('₀'..='₉').contains(&c) || c.is_ascii_digit() {
            if !seen_element {
                return false;
            }
            pos += c.len_utf8();
            continue;
        }
        if "-()+–".contains(c) {
            if c == '(' {
                open = true
            }
            pos += c.len_utf8();
            continue;
        }
        let element = include_str!("../../../data/ta/electronic/elements.tsv")
            .lines()
            .filter(|e| rest.starts_with(*e))
            .max_by_key(|e| e.len());
        if let Some(e) = element {
            seen_element = true;
            pos += e.len()
        } else {
            return false;
        }
    }
    seen_element && (open || !s.ends_with(')'))
}
fn allowed(c: char, class: &str) -> bool {
    if c.is_ascii_alphanumeric() || digit(c).is_some() || ('₀'..='₉').contains(&c) {
        return true;
    }
    match class {
        "url" => ".-_/+#?&=%:".contains(c),
        "unix" => ".-_/$".contains(c),
        "windows" => ".-_\\():".contains(c),
        _ => false,
    }
}
pub fn electronic(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let ascii = |c: char| c.is_ascii_alphanumeric();
    if let Some((u, d)) = s.split_once('@') {
        if !u.is_empty()
            && !d.is_empty()
            && u.chars().all(|c| ascii(c) || "._-".contains(c))
            && d.chars().all(|c| ascii(c) || ".-".contains(c))
        {
            out.push((format!("{} {}", f("username", u), f("domain", d)), 1.0));
        }
    }
    if chemical(s) {
        out.push((f("fragment_id", s), 1.2));
    }
    for (p, k, w) in [
        ("https://www.", "httpswww", 1.0),
        ("http://www.", "httpwww", 1.0),
        ("https://", "https", 1.01),
        ("http://", "http", 1.01),
        ("www.", "www", 1.02),
    ] {
        if let Some(d) = s.strip_prefix(p) {
            if !d.is_empty() && d.chars().all(|c| allowed(c, "url")) {
                out.push((format!("{} {}", f("protocol", k), f("domain", d)), 1.0 + w));
            }
        }
    }
    if s.contains('.') {
        let mut piece_count = 0;
        let mut ip = true;
        for part in s.split('.') {
            piece_count += 1;
            ip &= !part.is_empty() && part.len() <= 3 && part.chars().all(|c| c.is_ascii_digit());
        }
        if piece_count == 4 && ip {
            out.push((f("domain", s), 1.0));
        }
        let domain = s.strip_suffix('/').unwrap_or(s);
        let mut segments = domain.split('.');
        let first = segments.next().unwrap_or("");
        let mut count = 1;
        let mut valid = !first.is_empty() && first.chars().all(|c| ascii(c) || c == '-');
        let mut last = first;
        for part in segments {
            count += 1;
            valid &= !part.is_empty() && part.chars().all(|c| ascii(c) || c == '-');
            last = part;
        }
        if count >= 2 && valid && TLDS.contains(last) {
            out.push((f("domain", s), 1.1));
        }
        if let Some((stem, ext)) = s.split_once('.') {
            if !ext.contains('.')
                && !stem.is_empty()
                && stem.chars().all(|c| ascii(c) || matches!(c, '-' | '_'))
                && EXTENSIONS.contains(ext)
            {
                out.push((f("domain", s), 1.1));
            }
        }
    }
    let windows = s.len() >= 4
        && s.as_bytes()[0].is_ascii_alphabetic()
        && s.as_bytes()[1] == b':'
        && s.as_bytes()[2] == b'\\'
        && s[3..].chars().all(|c| c == ' ' || allowed(c, "windows"));
    let backslash = s
        .strip_prefix('\\')
        .is_some_and(|v| !v.is_empty() && v.chars().all(|c| c == ' ' || c == '\\' || (c != '/' && allowed(c, "unix"))));
    let unix = s.split_once('/').is_some_and(|(a, b)| {
        (!a.is_empty() || !b.is_empty())
            && a.chars().all(|c| c != '/' && allowed(c, "unix"))
            && b.chars().all(|c| allowed(c, "unix"))
    });
    if windows || backslash || unix {
        out.push((f("path", s), 1.0));
    }
    out
}
static SERIAL_TAMIL: LazyLock<[bool; 128]> = LazyLock::new(|| {
    let mut chars = [false; 128];
    for line in include_str!("../../../data/ta/serial/chars.tsv").lines() {
        if let Some(c) = line.chars().next() {
            if ('\u{b80}'..='\u{bff}').contains(&c) {
                chars[(c as usize) - 0x0b80] = true;
            }
        }
    }
    chars
});
fn tamil_serial(c: char) -> bool {
    ('\u{b80}'..='\u{bff}').contains(&c) && SERIAL_TAMIL[(c as usize) - 0x0b80]
}
fn code_num(s: &str) -> Option<String> {
    let len = s.chars().count();
    if len == 0 {
        return None;
    }
    if len >= 4 {
        return digitwise(s);
    }
    cardinal(s).and_then(|(v, leading)| (!leading).then_some(v))
}
fn version_words(s: &str, min_parts: usize) -> Option<String> {
    let mut count = 0;
    for part in s.split('.') {
        if !part.chars().all(|c| digit(c).is_some()) || super::number::recognize(part) != Some(false) {
            return None;
        }
        count += 1;
    }
    if count < min_parts {
        return None;
    }
    let mut result = String::new();
    for part in s.split('.') {
        if !result.is_empty() {
            result.push_str(" டாட் ");
        }
        super::number::write_final(&mut result, part)?;
    }
    Some(result)
}
pub fn serial(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    if s.contains('.') {
        if let Some(words) = version_words(s, 3) {
            out.push((words, -0.05));
        }
        if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) {
            if let Some(words) = version_words(v, 2) {
                out.push((format!("{} {}", letters("v").unwrap(), words), -0.05));
            }
        }
    }
    if let Some((base, exp)) = s.split_once('^') {
        let (neg, exp) = exp.strip_prefix('-').map(|x| (true, x)).unwrap_or((false, exp));
        let e = code_num(exp).or_else(|| letters(exp)).map(|e| if neg { format!("மைனஸ் {e}") } else { e });
        if let Some(b) = letters(base) {
            if exp == "2" && !neg {
                out.push((format!("{b} ஸ்கொயர்"), -0.01))
            } else if exp == "3" && !neg {
                out.push((format!("{b} க்யூப்"), -0.01))
            } else if let Some(e) = e.as_ref() {
                out.push((format!("{b} அடுக்கு {e}"), 0.0))
            }
        }
        if let (Some(b), Some(e)) = (code_num(base), e) {
            out.push((format!("{b} அடுக்கு {e}"), 1.0));
        }
    }
    if s.contains('.') {
        let mut parts = [""; 4];
        let mut len = 0;
        for part in s.split('.') {
            if len == parts.len() {
                len += 1;
                break;
            }
            parts[len] = part;
            len += 1;
        }
        let ipv4 =
            len == 4 && parts.iter().all(|p| !p.is_empty() && p.len() <= 3 && p.chars().all(|c| c.is_ascii_digit()));
        let dot_date = len == 3
            && (1..=2).contains(&parts[0].chars().count())
            && (1..=2).contains(&parts[1].chars().count())
            && parts[2].chars().count() == 4
            && parts[..3].iter().all(|p| p.chars().all(|c| digit(c).is_some()));
        if ipv4 || dot_date {
            return Vec::new();
        }
    }
    if s.chars().count() < 2 || s.chars().any(char::is_whitespace) || super::ordinal::recognize(s) {
        return out;
    }
    if s.chars().all(|c| c.is_ascii_alphabetic() || c == '-' || c == '/') {
        return out;
    }
    let mut is_measure = false;
    super::quantity::measure_costs(s, |_| is_measure = true);
    let mut is_money = false;
    if !is_measure {
        super::quantity::money_costs(s, |_| is_money = true);
    }
    if is_measure || is_money {
        return out;
    }
    if let Some(year) = s.strip_suffix('s').or_else(|| s.strip_suffix("கள்")).or_else(|| s.strip_suffix("-கள்"))
    {
        let y: Vec<_> = year.chars().collect();
        if y.len() == 4
            && matches!(digit(y[0]), Some(1 | 2))
            && y.iter().all(|c| digit(*c).is_some())
            && digit(y[3]) == Some(0)
        {
            return out;
        }
    }
    if let Some((a, b)) = s.split_once(|c| c == 'x' || c == 'X') {
        if !a.is_empty()
            && !b.is_empty()
            && a.chars().all(|c| digit(c).is_some())
            && b.chars().all(|c| digit(c).is_some())
        {
            return out;
        }
    }
    let mut result = String::new();
    let mut run = String::new();
    let mut kind = 0u8;
    let mut segments = 0;
    let mut explicit = false;
    for c in s.chars().chain(std::iter::once('\0')) {
        let k = if digit(c).is_some() {
            1
        } else if c.is_ascii_alphabetic() {
            2
        } else if ('\u{b80}'..='\u{bff}').contains(&c) && tamil_serial(c) {
            3
        } else if "#%&@".contains(c) {
            4
        } else {
            0
        };
        if k != kind || k == 0 || k == 4 {
            if !run.is_empty() {
                let value = match kind {
                    1 => {
                        if run.chars().count() >= 4 {
                            digitwise(&run)
                        } else {
                            cardinal(&run).and_then(|(v, l)| (!l).then_some(v))
                        }
                    }
                    2 => letters(&run),
                    3 => (run.chars().count() >= 2).then_some(run.clone()),
                    4 => lookup(include_str!("../../../data/ta/serial/special_symbols.tsv"), &run).map(str::to_owned),
                    _ => None,
                };
                let Some(value) = value else { return out };
                if segments > 0 && !explicit {
                    result.push(' ')
                }
                result.push_str(&value);
                segments += 1;
                run.clear();
                explicit = false;
            }
            kind = k;
        }
        if k > 0 {
            run.push(c)
        } else if c == '-' || c == '/' {
            if segments == 0 || explicit {
                return out;
            }
            result.push(c);
            explicit = true;
        } else if c != '\0' {
            return out;
        }
    }
    if segments >= 2 && !explicit {
        out.push((result, 0.0));
    }
    out
}

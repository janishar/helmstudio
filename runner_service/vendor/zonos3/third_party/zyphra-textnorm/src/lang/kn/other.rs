use super::graph_utils::{cardinal, digit, digitwise, table};
use super::numeric::Analysis;
const ABBR: &str = include_str!("../../../data/kn/whitelist/abbreviations.tsv");
static ABBR_INDEX: std::sync::LazyLock<std::collections::HashMap<&'static str, &'static str>> =
    std::sync::LazyLock::new(|| {
        let mut index = std::collections::HashMap::new();
        for (key, value) in ABBR.lines().filter_map(|line| line.split_once('\t')) {
            index.entry(key).or_insert(value);
        }
        index
    });
static ABBR_HAS_SPACES: std::sync::LazyLock<bool> = std::sync::LazyLock::new(|| {
    ABBR.lines().filter_map(|line| line.split_once('\t')).any(|(key, _)| key.contains(' '))
});
const LETTERS: &str = include_str!("../../../data/kn/address/letters.tsv");
const ROMAN: &str = include_str!("../../../data/kn/roman/roman_to_spoken.tsv");
const ROMAN_ORD: &str = include_str!("../../../data/kn/roman/roman_to_ordinal.tsv");
/// Source keys that can make a digit-free Kannada word relevant to a
/// classifier. The boolean marks keys which need a following space.
static WORD_KEYS: std::sync::LazyLock<std::collections::HashMap<char, Vec<(&'static str, bool)>>> =
    std::sync::LazyLock::new(|| {
        let mut words: std::collections::HashMap<char, Vec<(&'static str, bool)>> = std::collections::HashMap::new();
        for line in ABBR.lines() {
            let key = line.split('\t').next().unwrap_or("");
            let mut parts = key.split(' ');
            let first = parts.next().unwrap_or("");
            if let Some(c) = first.chars().next() {
                words.entry(c).or_default().push((first, parts.next().is_some()));
            }
        }
        for line in include_str!("../../../data/kn/date/month_names.tsv").lines() {
            let key = line.split('\t').next().unwrap_or("");
            if let Some(c) = key.chars().next() {
                words.entry(c).or_default().push((key, true));
            }
        }
        for &key in ROMAN_KEYS {
            if let Some(c) = key.chars().next() {
                words.entry(c).or_default().push((key, true));
            }
        }
        words
    });

static CONTEXT_INITIALS: std::sync::LazyLock<Vec<char>> = std::sync::LazyLock::new(|| {
    let mut initials: Vec<_> = [
        include_str!("../../../data/kn/telephone/credit_context.tsv"),
        include_str!("../../../data/kn/telephone/pincode_context.tsv"),
    ]
    .iter()
    .flat_map(|data| data.lines().filter_map(|line| line.chars().next()))
    .collect();
    initials.sort_unstable();
    initials.dedup();
    initials
});

pub fn might_classify_word(word: &[char], followed_by_space: bool) -> bool {
    if followed_by_space && word.iter().any(|c| CONTEXT_INITIALS.contains(c)) {
        return true;
    }
    WORD_KEYS.get(&word[0]).is_some_and(|keys| {
        keys.iter()
            .any(|(key, needs_space)| (!needs_space || followed_by_space) && key.chars().eq(word.iter().copied()))
    })
}
fn name(s: &str) -> String {
    format!("name: \"{}\"", s.replace(' ', "\u{a0}"))
}
fn letters(s: &str) -> Option<String> {
    if s.is_empty() {
        return None;
    }
    let mut result = Vec::new();
    for c in s.chars() {
        let k = c.to_ascii_lowercase().to_string();
        result.push(table(LETTERS, &k)?);
    }
    Some(result.join(" "))
}
fn kn(c: char) -> bool {
    matches!(c as u32, 0x0c80..=0x0cff) && digit(c).is_none() || c == '\u{200c}' || c == '\u{200d}'
}
fn code_number(s: &str) -> Option<String> {
    if s.chars().count() >= 4 { digitwise(s) } else { cardinal(s) }
}
fn serial(s: &str, out: &mut Vec<Analysis>) {
    if s.chars().count() < 2 || s.chars().any(char::is_whitespace) {
        return;
    }
    if let Some((a, b)) = s.split_once('^') {
        let stripped = b.strip_prefix('-').or_else(|| b.strip_prefix('−'));
        let neg = stripped.is_some();
        let b = stripped.unwrap_or(b);
        let exp = code_number(b).or_else(|| letters(b));
        if let Some(exp) = exp {
            if let Some(base) = code_number(a) {
                out.push((2_110_000, 0, name(&format!("{base} ಟು ದಿ ಪವರ್ {}{exp}", if neg { "ಋಣ " } else { "" }))));
            }
            if let Some(base) = letters(a) {
                let v = if !neg && b == "2" {
                    format!("{base} ವರ್ಗ")
                } else if !neg && b == "3" {
                    format!("{base} ಘನ")
                } else {
                    format!("{base} ರ {}{exp} ಘಾತ", if neg { "ಋಣ " } else { "" })
                };
                out.push((1_110_000 - if !neg && (b == "2" || b == "3") { 10_000 } else { 0 }, 0, name(&v)));
            }
        }
    }
    let (version, prefix) =
        if let Some(v) = s.strip_prefix('v').or_else(|| s.strip_prefix('V')) { (v, true) } else { (s, false) };
    let parts: Vec<_> = version.split('.').collect();
    let date_dot = parts.len() == 3
        && (1..=2).contains(&parts[0].len())
        && (1..=2).contains(&parts[1].len())
        && parts[2].len() == 4
        && parts.iter().all(|p| p.chars().all(|c| digit(c).is_some()));
    if !date_dot
        && (prefix && parts.len() == 2 || !prefix && parts.len() == 3)
        && parts.iter().all(|p| cardinal(p).is_some())
    {
        let v = parts.iter().filter_map(|p| cardinal(p)).collect::<Vec<_>>().join(" ಡಾಟ್ ");
        out.push((
            1_060_000,
            0,
            name(&if prefix { format!("{} {v}", letters(&s[..1]).unwrap_or_default()) } else { v }),
        ));
    }
    let all_digits = |x: &str| !x.is_empty() && x.chars().all(|c| digit(c).is_some());
    for sep in ['x', 'X'] {
        if let Some((a, b)) = s.split_once(sep) {
            if all_digits(a) && all_digits(b) {
                return;
            }
        }
    }
    for suffix in include_str!("../../../data/kn/ordinal/suffixes.tsv").lines() {
        let key = suffix.split('\t').next().unwrap_or("");
        if !key.is_empty() && s.strip_suffix(key).is_some_and(all_digits) {
            return;
        }
    }
    for units in [
        include_str!("../../../data/kn/measure/unit_other.tsv"),
        include_str!("../../../data/kn/measure/unit_prefixed_other.tsv"),
    ] {
        for row in units.lines() {
            let Some((unit, _)) = row.split_once('\t') else { continue };
            if let Some(n) = s.strip_suffix(unit) {
                if all_digits(n)
                    || n.split_once('-')
                        .or_else(|| n.split_once('–'))
                        .is_some_and(|(a, b)| all_digits(a) && all_digits(b))
                {
                    return;
                }
            }
        }
    }
    for row in include_str!("../../../data/kn/money/currency.tsv").lines() {
        let Some((sym, _)) = row.split_once('\t') else { continue };
        if s.strip_prefix(sym).is_some_and(all_digits) || s.strip_suffix(sym).is_some_and(all_digits) {
            return;
        }
    }
    for suffix in ["s", "ರ", "ರಲ್ಲಿ"] {
        if let Some(n) = s.strip_suffix(suffix) {
            let d: Vec<_> = n.chars().collect();
            if d.len() == 4
                && matches!(digit(d[0]), Some(1 | 2))
                && digit(d[3]) == Some(0)
                && d.iter().all(|c| digit(*c).is_some())
            {
                return;
            }
        }
    }
    if s.chars().all(|c| c.is_ascii_alphabetic() || c == '-' || c == '/') {
        return;
    }
    if s.contains('²') || s.contains('³') || s.contains("-^") || s.contains("/^") {
        let mut spoken = String::new();
        let mut segment = String::new();
        let mut valid = true;
        let mut count = 0;
        for c in s.chars().chain(std::iter::once('\0')) {
            if matches!(c, '-' | '/' | '\0') {
                let value = table(include_str!("../../../data/kn/serial/special_symbols.tsv"), &segment)
                    .map(str::to_owned)
                    .or_else(|| code_number(&segment))
                    .or_else(|| letters(&segment))
                    .or_else(|| (!segment.is_empty() && segment.chars().all(kn)).then(|| segment.clone()));
                if let Some(v) = value {
                    spoken.push_str(&v);
                    count += 1;
                } else {
                    valid = false;
                    break;
                }
                segment.clear();
                if c != '\0' {
                    spoken.push(c);
                }
            } else {
                segment.push(c);
            }
        }
        if valid && count >= 2 {
            out.push((1_110_000, 0, name(&spoken)));
        }
    }
    let mut result = String::new();
    let mut run = String::new();
    let mut mode = 0u8;
    let mut segments = 0;
    let flush = |run: &mut String, mode: u8, result: &mut String, segments: &mut usize| -> Option<()> {
        if run.is_empty() {
            return Some(());
        }
        let v = match mode {
            1 => code_number(run)?,
            2 => letters(run)?,
            3 => run.clone(),
            _ => return None,
        };
        if !result.is_empty() && !result.ends_with('-') && !result.ends_with('/') {
            result.push(' ');
        }
        result.push_str(&v);
        run.clear();
        *segments += 1;
        Some(())
    };
    for c in s.chars() {
        let next = if digit(c).is_some() {
            1
        } else if c.is_ascii_alphabetic() {
            2
        } else if kn(c) {
            3
        } else if c == '-' || c == '/' {
            4
        } else {
            return;
        };
        if next == 4 {
            if run.is_empty() || flush(&mut run, mode, &mut result, &mut segments).is_none() {
                return;
            }
            result.push(c);
            mode = 0;
        } else {
            if mode != 0 && mode != next {
                if mode != 1 && next != 1 {
                    return;
                }
                if flush(&mut run, mode, &mut result, &mut segments).is_none() {
                    return;
                }
            }
            mode = next;
            run.push(c);
        }
    }
    if !run.is_empty() && flush(&mut run, mode, &mut result, &mut segments).is_some() && segments >= 2 {
        out.push((1_110_000, 0, name(&result)));
    }
}
const ROMAN_KEYS: &[&str] = &[
    "ಅಧ್ಯಾಯ",
    "ಅಧ್ಯಾಯಂ",
    "ಭಾಗ",
    "ಭಾಗಂ",
    "ಸಂಪುಟ",
    "ಖಂಡ",
    "ತರಗತಿ",
    "ದರ್ಜೆ",
    "ವಿಶ್ವಯುದ್ಧ",
    "ಮಹಾಯುದ್ಧ",
    "ಚಂದ್ರಯಾನ",
    "ಆವೃತ್ತಿ",
    "ಶ್ರೇಣಿ",
    "ಶತಮಾನ",
    "ಹಂತ",
    "ಪರ್ವ",
    "ಸರಣಿ",
    "ಅಂಕ",
    "ರೂಪ",
    "ಮೆಟ್ಟಿಲು",
];
static ROMAN_KEY_INDEX: std::sync::LazyLock<std::collections::HashSet<&'static str>> =
    std::sync::LazyLock::new(|| ROMAN_KEYS.iter().copied().collect());
static CONTEXT_PREFIXES: std::sync::LazyLock<std::collections::HashSet<&'static str>> =
    std::sync::LazyLock::new(|| {
        let mut prefixes = std::collections::HashSet::new();
        for data in [
            include_str!("../../../data/kn/telephone/credit_context.tsv"),
            include_str!("../../../data/kn/telephone/pincode_context.tsv"),
        ] {
            for key in data.lines() {
                let word = key.split(' ').next().unwrap_or("");
                for (i, c) in word.char_indices() {
                    prefixes.insert(&word[..i + c.len_utf8()]);
                }
            }
        }
        prefixes
    });

/// Whether one space can still lead to an accepted contextual other-class token.
pub fn can_cross_space(first: &str) -> bool {
    if first.starts_with('+')
        || first.starts_with('(')
        || first.ends_with('.') && first.as_bytes().first().is_some_and(u8::is_ascii_uppercase)
    {
        return true;
    }
    if ROMAN_KEY_INDEX.contains(first)
        || first.as_bytes().first().is_some_and(u8::is_ascii_uppercase) && table(ROMAN, first).is_some()
    {
        return true;
    }
    for suffix in ["ನೇ", "ನೆ"] {
        if first.strip_suffix(suffix).is_some_and(|r| table(ROMAN_ORD, r).is_some()) {
            return true;
        }
    }
    if first.as_bytes().get(1..3) == Some(&b":\\"[..]) {
        return true;
    }
    if *ABBR_HAS_SPACES
        && ABBR
            .lines()
            .filter_map(|line| line.split_once('\t'))
            .any(|(key, _)| key.strip_prefix(first).is_some_and(|rest| rest.starts_with(' ')))
    {
        return true;
    }
    first.char_indices().any(|(i, _)| CONTEXT_PREFIXES.contains(&first[i..]))
}

fn roman(s: &str, out: &mut Vec<Analysis>) {
    for key in ROMAN_KEYS {
        for sep in [' ', '-'] {
            if let Some(r) = s.strip_prefix(key).and_then(|x| x.strip_prefix(sep)) {
                if table(ROMAN, r).is_some() {
                    out.push((
                        1_100_000,
                        0,
                        format!("roman {{ preserve_order: true key_cardinal: \"{key}\" integer: \"{r}\" }}"),
                    ));
                }
            }
            if let Some(r) = s.strip_suffix(key).and_then(|x| x.strip_suffix(sep)) {
                if table(ROMAN, r).is_some() {
                    out.push((
                        1_100_000,
                        0,
                        format!("roman {{ preserve_order: true integer: \"{r}\" key_cardinal: \"{key}\" }}"),
                    ));
                }
            }
        }
    }
    let (base, key) = s.split_once(' ').map_or((s, None), |(a, b)| (a, Some(b)));
    for suffix in ["ನೇ", "ನೆ"] {
        if let Some(r) = base.strip_suffix(suffix) {
            if let Some(v) = table(ROMAN_ORD, r) {
                if key.is_none() || key.is_some_and(|k| ROMAN_KEYS.contains(&k)) {
                    let k = key.map(|k| format!(" key_cardinal: \"{k}\"")).unwrap_or_default();
                    out.push((
                        1_100_000,
                        0,
                        format!("roman {{ preserve_order: true integer: \"{r}\" default_ordinal: \"{v}\"{k} }}"),
                    ));
                }
            }
        }
    }
}
fn telephone(s: &str, out: &mut Vec<Analysis>) {
    let digits: String = s.chars().filter(|c| digit(*c).is_some()).collect();
    if digits.is_empty() {
        return;
    }
    let count = digits.chars().count();
    if s.starts_with('+') && s[1..].chars().all(|c| digit(c).is_some() || matches!(c, ' ' | '-' | '.')) {
        let all: Vec<char> = s[1..].chars().collect();
        for code_len in 1..=3 {
            let mut j = 0;
            while j < all.len() && j < code_len && digit(all[j]).is_some() {
                j += 1;
            }
            if j != code_len {
                continue;
            }
            let cc: String = all[..j].iter().collect();
            let rest: String = all[j..].iter().filter(|c| digit(**c).is_some()).collect();
            if (6..=12).contains(&rest.chars().count()) {
                if let (Some(a), Some(b)) = (digitwise(&cc), digitwise(&rest)) {
                    out.push((
                        1_100_000,
                        0,
                        format!("telephone {{ country_code: \"ಪ್ಲಸ್ {a} \" number_part: \"{b} \" }}"),
                    ));
                }
            }
        }
    }
    if let Some(rest) = s.strip_prefix('+') {
        if (7..=15).contains(&count)
            && rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')'))
        {
            if let Some(v) = digitwise(&digits) {
                out.push((1_100_000, -1, name(&format!("ಪ್ಲಸ್ {v}"))));
            }
        }
    }
    for (contexts, min) in [
        (include_str!("../../../data/kn/telephone/credit_context.tsv"), 16),
        (include_str!("../../../data/kn/telephone/pincode_context.tsv"), 6),
    ] {
        for keyword in contexts.lines().filter(|v| !v.is_empty()) {
            if let Some(pos) = s.find(keyword) {
                let end = pos + keyword.len();
                let before = &s[..pos];
                let after = &s[end..];
                if before.chars().all(|c| digit(c).is_none() && !c.is_whitespace()) {
                    let sep = after.len() - after.trim_start_matches([' ', ':', '-']).len();
                    let number = &after[sep..];
                    let n = number.chars().count();
                    if sep > 0 && (min..=19).contains(&n) {
                        if let Some(v) = digitwise(number) {
                            out.push((
                                1_100_000,
                                0,
                                format!("telephone {{ number_part: \"{}{v}\" }}", &s[..end + sep]),
                            ));
                        }
                    }
                }
                let number = before.trim_end_matches([' ', ':', '-']);
                if number.len() < before.len()
                    && after.chars().all(|c| digit(c).is_none() && !c.is_whitespace())
                    && (min..=19).contains(&number.chars().count())
                {
                    if let Some(v) = digitwise(number) {
                        out.push((1_100_000, 0, format!("telephone {{ number_part: \"{v}{}\" }}", &s[number.len()..])));
                    }
                }
            }
        }
    }
    if s.chars().all(|c| digit(c).is_some() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
        let first = digits.chars().next().and_then(digit).unwrap_or(0);
        let leading_zero = count == 11 && first == 0;
        let first_mobile = if leading_zero { digits.chars().nth(1).and_then(digit) } else { Some(first) };
        let groups: Vec<_> = s.split(|c| matches!(c, ' ' | '-' | '.')).collect();
        let valid_groups = groups.len() == 1
            || groups.len() == 2
                && groups[0].chars().count() == 5 + usize::from(leading_zero)
                && groups[1].chars().count() == 5
            || groups.len() == 3
                && groups[0].chars().count() == 3 + usize::from(leading_zero)
                && groups[1].chars().count() == 3
                && groups[2].chars().count() == 4;
        let mobile = (count == 10 || leading_zero)
            && first_mobile.is_some_and(|n| n >= 6)
            && valid_groups
            && !s.contains('(')
            && !s.contains(')');
        let landline = if groups.len() == 2 {
            let std = groups[0].strip_prefix('(').and_then(|v| v.strip_suffix(')')).unwrap_or(groups[0]);
            first == 0
                && (2..=5).contains(&std.chars().count())
                && (6..=8).contains(&groups[1].chars().count())
                && std.chars().all(|c| digit(c).is_some())
                && groups[1].chars().all(|c| digit(c).is_some())
        } else {
            false
        };
        if mobile || landline {
            if let Some(v) = digitwise(&digits) {
                out.push((
                    if mobile { 900_000 } else { 1_100_000 },
                    0,
                    format!("telephone {{ number_part: \"{v} \" }}"),
                ));
            }
        }
    }
}
fn electronic_char(c: char, class: &str) -> bool {
    if c.is_ascii_alphanumeric()
        || digit(c).is_some()
        || table(include_str!("../../../data/kn/electronic/subscript_digit.tsv"), &c.to_string()).is_some()
    {
        return true;
    }
    include_str!("../../../data/kn/electronic/symbol_classes.tsv")
        .lines()
        .filter_map(|l| l.split_once('\t'))
        .any(|(k, v)| k == c.to_string() && v.split(',').any(|item| item == class))
}
fn electronic(s: &str, out: &mut Vec<Analysis>) {
    if let Some((user, domain)) = s.split_once('@') {
        if !user.is_empty()
            && user.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-' | '_'))
            && !domain.is_empty()
            && domain.chars().all(|c| c.is_ascii_alphanumeric() || matches!(c, '.' | '-'))
        {
            out.push((2_100_000, 0, format!("electronic {{ username: \"{user}\" domain: \"{domain}\" }}")));
        }
    }
    for (pre, proto, w) in [
        ("https://www.", "httpswww", 1_000_000),
        ("http://www.", "httpwww", 1_000_000),
        ("https://", "https", 1_010_000),
        ("http://", "http", 1_010_000),
        ("www.", "www", 1_020_000),
    ] {
        if let Some(domain) = s.strip_prefix(pre) {
            if !domain.is_empty() && domain.chars().all(|c| electronic_char(c, "url")) {
                out.push((2_110_000 + w, 0, format!("electronic {{ protocol: \"{proto}\" domain: \"{domain}\" }}")));
            }
        }
    }
    let parts: Vec<_> = s.split('.').collect();
    if parts.len() == 4 && parts.iter().all(|p| !p.is_empty() && p.len() <= 3 && p.chars().all(|c| c.is_ascii_digit()))
    {
        out.push((2_120_000, 0, format!("electronic {{ domain: \"{s}\" }}")));
    }
    if parts.len() == 2
        && parts[0].len() >= 2
        && parts[0].chars().all(|c| c.is_ascii_alphabetic())
        && table(include_str!("../../../data/kn/electronic/domain.tsv"), parts[1]).is_some()
    {
        out.push((2_130_000, 0, format!("electronic {{ domain: \"{s}\" }}")));
    }
    if (s.starts_with('/') || s.starts_with("./") || s.starts_with("../") || s.contains('/'))
        && s.len() > 1
        && s.chars().all(|c| electronic_char(c, "unix"))
    {
        out.push((2_150_000, 0, format!("electronic {{ path: \"{s}\" }}")));
    }
    if s.len() > 3
        && s.as_bytes()[0].is_ascii_alphabetic()
        && s.as_bytes()[1] == b':'
        && s.as_bytes()[2] == b'\\'
        && s[3..].chars().all(|c| c == ' ' || electronic_char(c, "windows"))
    {
        out.push((2_140_000, 0, format!("electronic {{ path: \"{s}\" }}")));
    }
}
pub fn analyze(s: &str, out: &mut Vec<Analysis>) {
    let has_space = s.contains(' ');
    if !has_space || *ABBR_HAS_SPACES {
        if let Some(v) = ABBR_INDEX.get(s) {
            out.push((1_010_000, 0, name(v)));
        }
    }
    if s.as_bytes().first().is_some_and(u8::is_ascii_uppercase) && s.contains('.') {
        for separator in [".", ". "] {
            let dotted: Vec<_> = s.strip_suffix('.').unwrap_or(s).split(separator).collect();
            if dotted.len() >= 3 && dotted.iter().all(|v| v.len() == 1 && v.bytes().all(|b| b.is_ascii_uppercase())) {
                out.push((1_010_000, 0, name(&dotted.join(""))));
            }
        }
    }
    if s.bytes().any(|b| matches!(b, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M')) {
        roman(s, out);
    }
    let first = s.chars().next();
    let has_context = s.contains("ಕಾರ್ಡ್") || s.contains("ಖಾತೆ") || s.contains("ಪಿನ್");
    if s.chars().any(|c| digit(c).is_some())
        && (has_context
            || (first.is_some_and(|c| matches!(c, '+' | '('))
                || first.is_some_and(|c| digit(c).is_some()) && s.chars().count() >= 9)
                && s.chars().all(|c| digit(c).is_some() || matches!(c, '+' | '(' | ')' | ' ' | '-' | '.')))
    {
        telephone(s, out);
    }
    if first.is_some_and(|c| c.is_ascii() || digit(c).is_some() || matches!(c, '₀'..='₉'))
        && (!has_space || s.as_bytes().get(1..3) == Some(&b":\\"[..]))
        && s.chars().any(|c| matches!(c, '.' | '@' | '/' | '\\'))
    {
        electronic(s, out);
    }
    if !s.chars().any(char::is_whitespace)
        && s.chars().any(|c| digit(c).is_none())
        && s.chars().any(|c| digit(c).is_some() || matches!(c, '-' | '/' | '^' | '²' | '³'))
    {
        serial(s, out);
    }
}

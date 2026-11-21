//! Kannada cardinal morphology shared by the semiotic classes.
use std::sync::LazyLock;

static TWO_DIGIT_NAMES: LazyLock<[&'static str; 100]> = LazyLock::new(|| {
    let mut names = [""; 100];
    for line in include_str!("../../../data/kn/numbers/teens_and_ties.tsv").lines() {
        if let Some((key, value)) = line.split_once('\t') {
            if let Ok(n) = key.parse::<usize>() {
                if n < names.len() {
                    names[n] = value;
                }
            }
        }
    }
    names
});

const DIGITS: [&str; 10] = ["ಸೊನ್ನೆ", "ಒಂದು", "ಎರಡು", "ಮೂರು", "ನಾಲ್ಕು", "ಐದು", "ಆರು", "ಏಳು", "ಎಂಟು", "ಒಂಬತ್ತು"];
const HUNDREDS: [&str; 10] = ["", "ನೂರು", "ಇನ್ನೂರು", "ಮುನ್ನೂರು", "ನಾನೂರು", "ಐನೂರು", "ಆರುನೂರು", "ಏಳುನೂರು", "ಎಂಟುನೂರು", "ಒಂಬೈನೂರು"];
const HUNDREDS_COMB: [&str; 10] = ["", "ನೂರ", "ಇನ್ನೂರ", "ಮುನ್ನೂರ", "ನಾನೂರ", "ಐನೂರ", "ಆರುನೂರ", "ಏಳುನೂರ", "ಎಂಟುನೂರ", "ಒಂಬೈನೂರ"];

/// Convert one of the source grammar's ASCII or Kannada digits to a digit value.
pub fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '೦'..='೯' => Some((c as u32 - '೦' as u32) as u8),
        _ => None,
    }
}

pub fn digit_name(n: u8) -> &'static str {
    DIGITS[n as usize]
}

fn two(n: u64) -> &'static str {
    if n < 10 {
        return DIGITS[n as usize];
    }
    TWO_DIGIT_NAMES[n as usize]
}

fn append_spell(out: &mut String, n: u64) {
    if n < 100 {
        out.push_str(two(n));
        return;
    }
    if n < 1_000 {
        let h = (n / 100) as usize;
        let rest = n % 100;
        if rest == 0 {
            out.push_str(HUNDREDS[h]);
        } else {
            out.push_str(HUNDREDS_COMB[h]);
            out.push(' ');
            out.push_str(two(rest));
        }
        return;
    }
    let (scale, exact, combining) = if n < 100_000 {
        (1_000, "ಸಾವಿರ", "ಸಾವಿರದ")
    } else if n < 10_000_000 {
        (100_000, "ಲಕ್ಷ", "ಲಕ್ಷದ")
    } else if n < 1_000_000_000 {
        (10_000_000, "ಕೋಟಿ", "ಕೋಟಿಯ")
    } else if n < 100_000_000_000 {
        (1_000_000_000, "ಅರಬ್", "ಅರಬ್")
    } else {
        (100_000_000_000, "ಖರಬ್", "ಖರಬ್")
    };
    append_spell(out, n / scale);
    let remainder = n % scale;
    out.push(' ');
    if remainder == 0 {
        out.push_str(exact);
    } else {
        out.push_str(combining);
        out.push(' ');
        append_spell(out, remainder);
    }
}

fn spell(n: u64) -> String {
    let mut result = String::new();
    append_spell(&mut result, n);
    result
}

/// Render a validated bare cardinal directly into the winning tag.
pub fn append_plain_cardinal(out: &mut String, chars: &[char]) {
    let first = usize::from(chars.first().is_some_and(|c| matches!(c, '-' | '−')));
    if first != 0 {
        out.push_str("negative: \"true\" ");
    }
    out.push_str("integer: \"");
    let digits = &chars[first..];
    if digits.len() > 1 && digit(digits[0]) == Some(0) {
        for (i, &c) in digits.iter().enumerate() {
            if i != 0 {
                out.push(' ');
            }
            out.push_str(digit_name(digit(c).expect("validated digit")));
        }
    } else {
        let mut n = 0u64;
        for &c in digits {
            n = n * 10 + digit(c).expect("validated digit") as u64;
        }
        append_spell(out, n);
    }
    out.push('"');
}

/// Source `graph_without_leading_zeros`, up to the seven-digit kharab head.
pub fn cardinal(input: &str) -> Option<String> {
    let mut n = 0u64;
    let mut count = 0;
    for c in input.chars() {
        let d = digit(c)?;
        if count == 0 && d == 0 && input.chars().count() != 1 {
            return None;
        }
        n = n.checked_mul(10)?.checked_add(d as u64)?;
        count += 1;
        if count > 18 {
            return None;
        }
    }
    if count == 0 { None } else { Some(spell(n)) }
}

pub fn digitwise(input: &str) -> Option<String> {
    let mut out = String::new();
    for c in input.chars() {
        let d = digit(c)?;
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(digit_name(d));
    }
    if out.is_empty() { None } else { Some(out) }
}

pub fn table<'a>(table: &'a str, key: &str) -> Option<&'a str> {
    table
        .lines()
        .filter_map(|line| {
            if line.is_empty() || line.starts_with('#') {
                return None;
            }
            let (k, v) = line.split_once('\t').unwrap_or((line, line));
            let k = k.strip_prefix('\\').unwrap_or(k);
            (k == key).then_some(v.split('\t').next().unwrap_or(v))
        })
        .next()
}

pub fn ascii_digits(s: &str) -> Option<String> {
    s.chars().map(|c| digit(c).map(|n| (b'0' + n) as char)).collect()
}

/// Cardinal final graph, including grouped and leading-zero forms and their weight.
pub fn final_cardinal(s: &str) -> Option<(String, i64)> {
    if s.contains(',') {
        let parts: Vec<_> = s.split(',').collect();
        if parts.len() < 2 {
            return None;
        }
        let widths: Vec<_> = parts.iter().map(|p| p.chars().count()).collect();
        let western = (1..=3).contains(&widths[0]) && widths[1..].iter().all(|&w| w == 3);
        let indian =
            (1..=2).contains(&widths[0]) && *widths.last()? == 3 && widths[1..widths.len() - 1].iter().all(|&w| w == 2);
        if !western && !indian {
            return None;
        }
        let joined = parts.concat();
        return cardinal(&joined).map(|s| (s, 0));
    }
    if s.chars().count() > 1 && digit(s.chars().next()?) == Some(0) {
        return digitwise(s).map(|v| (v, 500_000));
    }
    cardinal(s).map(|v| (v, 0))
}

pub fn ordinal(s: &str) -> Option<String> {
    let a = ascii_digits(s)?;
    if a.is_empty() || a.starts_with('0') || a.len() > 7 {
        return None;
    }
    let n: u64 = a.parse().ok()?;
    ordinal_num(n)
}

fn ordinal_num(n: u64) -> Option<String> {
    if n < 100 {
        return table(include_str!("../../../data/kn/ordinal/ordinals.tsv"), &n.to_string()).map(str::to_owned);
    }
    if n < 1000 {
        let prefix = HUNDREDS_COMB[(n / 100) as usize];
        let r = n % 100;
        return if r == 0 { Some(format!("{prefix}ನೇ")) } else { Some(format!("{prefix} {}", ordinal_num(r)?)) };
    }
    let (scale, combining) = if n < 100_000 { (1_000, "ಸಾವಿರದ") } else { (100_000, "ಲಕ್ಷದ") };
    let h = n / scale;
    let r = n % scale;
    if n == 1000 {
        return Some("ಸಾವಿರದನೇ".to_owned());
    }
    let head = spell(h);
    if r == 0 {
        Some(format!("{head} {combining}ನೇ"))
    } else {
        Some(format!("{head} {combining} {}", ordinal_num(r)?))
    }
}

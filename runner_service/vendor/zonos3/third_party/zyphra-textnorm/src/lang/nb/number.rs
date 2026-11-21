//! Productive Norwegian cardinal and ordinal morphology.

const SMALL: [&str; 20] = [
    "null", "en", "to", "tre", "fire", "fem", "seks", "sju", "åtte", "ni", "ti", "elleve", "tolv", "tretten",
    "fjorten", "femten", "seksten", "sytten", "atten", "nitten",
];
const TENS: [&str; 10] = ["", "", "tjue", "tretti", "førti", "femti", "seksti", "sytti", "åtti", "nitti"];
const ORD: [&str; 20] = [
    "nullte",
    "første",
    "andre",
    "tredje",
    "fjerde",
    "femte",
    "sjette",
    "sjuende",
    "åttende",
    "niende",
    "tiende",
    "ellevte",
    "tolvte",
    "trettende",
    "fjortende",
    "femtende",
    "sekstende",
    "syttende",
    "attende",
    "nittende",
];

fn under_hundred(out: &mut String, n: u16, neuter: bool) {
    if n < 20 {
        out.push_str(if n == 1 && neuter { "ett" } else { SMALL[n as usize] });
        return;
    }
    out.push_str(TENS[(n / 10) as usize]);
    if n % 10 != 0 {
        out.push_str(if n % 10 == 1 && neuter { "ett" } else { SMALL[(n % 10) as usize] });
    }
}

fn under_thousand(out: &mut String, n: u16, neuter: bool) {
    if n < 100 {
        under_hundred(out, n, neuter);
        return;
    }
    if n / 100 != 1 {
        out.push_str(SMALL[(n / 100) as usize]);
        out.push(' ');
    }
    out.push_str("hundre");
    if n % 100 != 0 {
        out.push_str(" og ");
        under_hundred(out, n % 100, neuter);
    }
}

/// The source accepts at most 15 digits, and grouping separators only before a three-digit group.
pub fn parse_cardinal(input: &str) -> Option<u64> {
    let bytes = input.as_bytes();
    if bytes.is_empty() {
        return None;
    }
    let mut n = 0u64;
    let mut count = 0usize;
    let mut leading_zero = false;
    for (i, &b) in bytes.iter().enumerate() {
        if b.is_ascii_digit() {
            if count == 0 {
                leading_zero = b == b'0';
            }
            count += 1;
            if count > 15 || (leading_zero && count > 1) {
                return None;
            }
            n = n * 10 + u64::from(b - b'0');
        } else if (b == b'.' || b == b' ')
            && i > 0
            && i + 4 <= bytes.len()
            && bytes[i + 1..i + 4].iter().all(u8::is_ascii_digit)
            && (i + 4 == bytes.len() || bytes[i + 4] == b'.' || bytes[i + 4] == b' ')
        {
        } else {
            return None;
        }
    }
    if count == 0 {
        return None;
    }
    Some(n)
}

pub fn cardinal(input: &str, neuter: bool) -> Option<String> {
    let n = parse_cardinal(input)?;
    if n == 0 {
        return Some("null".into());
    }
    let scales = [
        (1_000_000_000_000u64, "billion", "billioner"),
        (1_000_000_000, "milliard", "milliarder"),
        (1_000_000, "million", "millioner"),
    ];
    let mut rest = n;
    let mut out = String::new();
    for (scale, singular, plural) in scales {
        let part = (rest / scale) as u16;
        rest %= scale;
        if part == 0 {
            continue;
        }
        if !out.is_empty() {
            out.push(' ');
        }
        if part == 1 {
            out.push_str(if out.is_empty() { "en " } else { "én " });
            out.push_str(singular);
        } else {
            under_thousand(&mut out, part, neuter);
            out.push(' ');
            out.push_str(plural);
        }
    }
    let thousands = (rest / 1000) as u16;
    let last = (rest % 1000) as u16;
    if thousands != 0 {
        if !out.is_empty() {
            out.push(' ');
        }
        if thousands == 1 {
            if n >= 1_000_000 {
                out.push_str("ett tusen");
            } else {
                out.push_str("tusen");
            }
        } else {
            under_thousand(&mut out, thousands, neuter);
            out.push_str(" tusen");
        }
    }
    if last != 0 {
        if !out.is_empty() {
            out.push_str(if last < 100 { " og " } else { " " });
        }
        under_thousand(&mut out, last, neuter);
    }
    Some(out)
}

/// The graph additionally weights explicit \"ett hundre\" and alternate thousand heads.
pub fn cardinal_variants(input: &str, neuter: bool) -> Vec<(String, f64)> {
    let Some(base) = cardinal(input, neuter) else { return Vec::new() };
    if !base.contains("hundre") && !base.contains("tusen") {
        return vec![(base, 0.0)];
    }
    let words: Vec<&str> = base.split(' ').collect();
    let mut options: Vec<(Vec<String>, f64)> = vec![(Vec::new(), 0.0)];
    let mut i = 0;
    while i < words.len() {
        let previous_scale = i == 0
            || matches!(
                words[i - 1],
                "million" | "millioner" | "milliard" | "milliarder" | "billion" | "billioner" | "tusen"
            );
        let mut choices = vec![(words[i].to_owned(), 0.0, 1usize)];
        if words[i] == "hundre" && previous_scale {
            choices.push(("ett hundre".into(), 0.1, 1));
        }
        if words[i] == "tusen" && i == 0 {
            choices.push(("ett tusen".into(), 0.1, 1));
        }
        if words[i] == "ett" && words.get(i + 1) == Some(&"tusen") && previous_scale {
            choices = vec![("ett tusen".into(), 0.0, 2), ("tusen".into(), 0.1, 2)];
        }
        let advance = choices[0].2;
        let mut next = Vec::new();
        for (prefix, cost) in options {
            for (word, extra, _) in &choices {
                let mut value = prefix.clone();
                value.push(word.clone());
                next.push((value, cost + extra));
            }
        }
        options = next;
        i += advance;
    }
    options.into_iter().map(|(words, cost)| (words.join(" "), cost)).collect()
}

pub fn ordinal(input: &str) -> Option<String> {
    if input.is_empty() || !input.bytes().all(|b| b.is_ascii_digit()) || (input.len() > 1 && input.starts_with('0')) {
        return None;
    }
    let n: u32 = input.parse().ok()?;
    if n < 20 {
        return Some(ORD[n as usize].into());
    }
    if n < 100 {
        let mut s = TENS[(n / 10) as usize].to_owned();
        s.push_str(if n % 10 == 0 { "ende" } else { ORD[(n % 10) as usize] });
        return Some(s);
    }
    if n == 100 {
        return Some("hundrede".into());
    }
    if n == 1000 {
        return Some("tusende".into());
    }
    if n == 1_000_000 {
        return Some("millionte".into());
    }
    if n < 1000 && n % 100 != 0 {
        let prefix = if n / 100 == 1 { "hundre".to_owned() } else { format!("{} hundre", SMALL[(n / 100) as usize]) };
        return Some(format!("{} og {}", prefix, ordinal(&(n % 100).to_string())?));
    }
    None
}

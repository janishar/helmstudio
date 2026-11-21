//! Productive Georgian cardinal morphology and case-suffix attachment.

const SMALL: [&str; 20] = [
    "ნული",
    "ერთი",
    "ორი",
    "სამი",
    "ოთხი",
    "ხუთი",
    "ექვსი",
    "შვიდი",
    "რვა",
    "ცხრა",
    "ათი",
    "თერთმეტი",
    "თორმეტი",
    "ცამეტი",
    "თოთხმეტი",
    "თხუთმეტი",
    "თექვსმეტი",
    "ჩვიდმეტი",
    "თვრამეტი",
    "ცხრამეტი",
];
pub fn digit_word(digit: u8) -> &'static str {
    SMALL[(digit - b'0') as usize]
}
const HUNDREDS: [&str; 10] = ["", "ას", "ორას", "სამას", "ოთხას", "ხუთას", "ექვსას", "შვიდას", "რვაას", "ცხრაას"];

fn write_under_thousand(n: usize, out: &mut String) {
    if n < 20 {
        out.push_str(SMALL[n]);
    } else if n < 100 {
        let stems = ["", "ოც", "ორმოც", "სამოც", "ოთხმოც"];
        out.push_str(stems[n / 20]);
        if n % 20 == 0 {
            out.push('ი');
        } else {
            out.push_str("და");
            out.push_str(SMALL[n % 20]);
        }
    } else {
        out.push_str(HUNDREDS[n / 100]);
        if n % 100 == 0 {
            out.push('ი');
        } else {
            out.push(' ');
            write_under_thousand(n % 100, out);
        }
    }
}

fn under_thousand(n: usize) -> String {
    let mut out = String::new();
    write_under_thousand(n, &mut out);
    out
}

fn write_cardinal(n: u64, out: &mut String) {
    if n < 1000 {
        write_under_thousand(n as usize, out);
        return;
    }
    let powers = [1_000_000_000_000u64, 1_000_000_000, 1_000_000, 1_000];
    let stems = ["ტრილიონ", "მილიარდ", "მილიონ", "ათას"];
    let mut remaining = n;
    let mut needs_space = false;
    for (power, stem) in powers.into_iter().zip(stems) {
        let group = (remaining / power) as usize;
        remaining %= power;
        if group == 0 {
            continue;
        }
        if needs_space {
            out.push(' ');
        }
        if power != 1000 || group != 1 {
            write_under_thousand(group, out);
            out.push(' ');
        }
        out.push_str(stem);
        if remaining == 0 {
            out.push('ი');
        }
        needs_space = true;
    }
    if remaining != 0 {
        if needs_space {
            out.push(' ');
        }
        write_under_thousand(remaining as usize, out);
    }
}

/// The source's ungrouped `all_nums_no_tokens`: zero or up to fifteen digits,
/// without leading zeroes.
pub fn cardinal(input: &str) -> Option<String> {
    if input.is_empty()
        || input.len() > 15
        || !input.bytes().all(|b| b.is_ascii_digit())
        || (input.len() > 1 && input.starts_with('0'))
    {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    let mut out = String::new();
    write_cardinal(n, &mut out);
    Some(out)
}

/// Implements the ordered suffix rewrites in `ka/graph_utils.py`.
pub fn glue_suffix(word: &str, suffix: &str) -> String {
    let rules: &[(&str, &str)] = if word.ends_with('ი') {
        &[
            ("დან", "იდან"),
            ("მდე", "ამდე"),
            ("იდან", "იდან"),
            ("ამდე", "ამდე"),
            ("ით", "ით"),
            ("ის", "ის"),
            ("ად", "ად"),
            ("ს", "ს"),
            ("ზე", "ზე"),
            ("ში", "ში"),
            ("მა", "მა"),
            ("თან", "თან"),
            ("კენ", "კენ"),
            ("თვის", "თვის"),
            ("ისთვის", "ისთვის"),
            ("იანი", "იანი"),
            ("იანები", "იანები"),
            ("იანებში", "იანებში"),
            ("იანების", "იანების"),
        ]
    } else if word.ends_with('ა') {
        &[
            ("დან", "იდან"),
            ("იდან", "იდან"),
            ("მდე", "ამდე"),
            ("ამდე", "ამდე"),
            ("ით", "ით"),
            ("ის", "ის"),
            ("ს", "ს"),
            ("ზე", "ზე"),
            ("ში", "ში"),
            ("მა", "მ"),
        ]
    } else if word.ends_with('ე') {
        &[
            ("ეს", "ეს"),
            ("ემ", "ემ"),
            ("ე", "ე"),
            ("ის", "ის"),
            ("ით", "ით"),
            ("ად", "ედ"),
            ("ედ", "ედ"),
            ("მ", "მ"),
            ("მა", "მ"),
            ("ს", "ს"),
        ]
    } else if word.ends_with('ო') {
        &[("ით", "ოთი")]
    } else {
        &[]
    };
    for &(from, to) in rules {
        if let Some(rest) = suffix.strip_prefix(from) {
            let last = word.chars().last().unwrap().len_utf8();
            return format!("{}{to}{rest}", &word[..word.len() - last]);
        }
    }
    format!("{word}{suffix}")
}

/// `number`, including the separately weighted leading-zero path.
pub fn number(input: &str) -> Option<(String, f64)> {
    let mut out = String::new();
    let weight = number_into(input, &mut out)?;
    Some((out, weight))
}

/// Append the source number reading without allocating intermediate groups.
pub fn number_into(input: &str, out: &mut String) -> Option<f64> {
    if input.len() > 1 && input.starts_with('0') && input.bytes().all(|b| b.is_ascii_digit()) {
        for (i, b) in input.bytes().enumerate() {
            if i != 0 {
                out.push(' ');
            }
            out.push_str(SMALL[(b - b'0') as usize]);
        }
        return Some(0.1);
    }
    if input.is_empty() {
        return None;
    }
    if input.bytes().all(|b| b.is_ascii_digit()) {
        if input.len() > 15 {
            return None;
        }
        let n = input.parse().ok()?;
        write_cardinal(n, out);
        return Some(0.0);
    }
    let mut groups = input.split(|c| matches!(c, ' ' | '\u{a0}' | ',' | '.'));
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut n: u64 = first.parse().ok()?;
    let mut digits = first.len();
    let mut found = false;
    for group in groups {
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        digits += 3;
        if digits > 15 {
            return None;
        }
        n = n * 1000 + group.parse::<u64>().ok()?;
        found = true;
    }
    if !found {
        return None;
    }
    write_cardinal(n, out);
    Some(0.01)
}

/// The unsigned cardinal graph, including grouped forms but excluding the
/// separate digit-by-digit leading-zero branch.
pub fn all_number(input: &str) -> Option<(String, f64)> {
    let (value, weight) = number(input)?;
    if weight == 0.1 { None } else { Some((value, weight)) }
}
const ORDINAL_SMALL: [&str; 20] = [
    "",
    "პირველი",
    "მეორე",
    "მესამე",
    "მეოთხე",
    "მეხუთე",
    "მეექვსე",
    "მეშვიდე",
    "მერვე",
    "მეცხრე",
    "მეათე",
    "მეთერთმეტე",
    "მეთორმეტე",
    "მეცამეტე",
    "მეთოთხმეტე",
    "მეთხუთმეტე",
    "მეთექვსმეტე",
    "მეჩვიდმეტე",
    "მეთვრამეტე",
    "მეცხრამეტე",
];

fn ordinal_under_thousand(n: usize, compound: bool) -> String {
    if n < 20 {
        return if n == 1 && compound { "მეერთე" } else { ORDINAL_SMALL[n] }.to_owned();
    }
    if n < 100 {
        let base = n / 20;
        let rem = n % 20;
        if rem == 0 {
            return ["", "მეოცე", "მეორმოცე", "მესამოცე", "მეოთხმოცე"][base].to_owned();
        }
        return format!("{}და{}", ["", "ოც", "ორმოც", "სამოც", "ოთხმოც"][base], ordinal_under_thousand(rem, true));
    }
    let h = n / 100;
    let rem = n % 100;
    if rem == 0 {
        return [
            "",
            "მეასე",
            "მეორასე",
            "მესამასე",
            "მეოთხასე",
            "მეხუთასე",
            "მეექვსასე",
            "მეშვიდასე",
            "მერვაასე",
            "მეცხრაასე",
        ][h]
            .to_owned();
    }
    format!("{} {}", HUNDREDS[h], ordinal_under_thousand(rem, true))
}

/// Cardinal-dependent ordinal graph, accepting one through 999999.
pub fn ordinal(input: &str) -> Option<String> {
    if input.is_empty() || input.len() > 6 || !input.bytes().all(|b| b.is_ascii_digit()) || input.starts_with('0') {
        return None;
    }
    let n: usize = input.parse().ok()?;
    if n == 0 {
        return None;
    }
    if n < 1000 {
        return Some(ordinal_under_thousand(n, false));
    }
    let group = n / 1000;
    let rem = n % 1000;
    if rem == 0 {
        return Some(if group == 1 {
            "მეათასე".to_owned()
        } else {
            format!("{} მეათასე", under_thousand(group))
        });
    }
    let prefix = if group == 1 { "ათას".to_owned() } else { format!("{} ათას", under_thousand(group)) };
    Some(format!("{prefix} {}", ordinal_under_thousand(rem, true)))
}

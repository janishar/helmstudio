// Number names and morphology derive from Unicode CLDR release 49 (Unicode-3.0).
//! Tamil cardinal morphology, corresponding to `ta/taggers/cardinal.py`.
use std::sync::LazyLock;

static SMALL_NUMBERS: LazyLock<[&'static str; 90]> = LazyLock::new(|| {
    let mut values = [""; 90];
    for (i, line) in include_str!("../../../data/ta/numbers/teens_and_ties.tsv").lines().enumerate() {
        if i == values.len() {
            break;
        }
        if let Some((_, value)) = line.split_once('\t') {
            values[i] = value;
        }
    }
    values
});

const DIGITS: [&str; 10] = ["பூஜ்யம்", "ஒன்று", "இரண்டு", "மூன்று", "நான்கு", "ஐந்து", "ஆறு", "ஏழு", "எட்டு", "ஒன்பது"];
const PREFIX: [&str; 20] = [
    "",
    "ஒன்ற",
    "இரண்ட",
    "மூன்ற",
    "நான்க",
    "ஐந்த",
    "ஆற",
    "ஏழ",
    "எட்ட",
    "ஒன்பத",
    "பத்த",
    "பதினொன்ற",
    "பன்னிரண்ட",
    "பதின்மூன்ற",
    "பதினான்க",
    "பதினைந்த",
    "பதினாற",
    "பதினேழ",
    "பதினெட்ட",
    "பத்தொன்பத",
];
const TENS: [&str; 10] = ["", "", "இருப", "முப்ப", "நாற்ப", "ஐம்ப", "அறுப", "எழுப", "எண்ப", "தொண்ணூற"];
const HUNDREDS: [&str; 10] =
    ["", "நூறு", "இருநூறு", "முந்நூறு", "நானூறு", "ஐநூறு", "அறுநூறு", "எழுநூறு", "எண்நூறு", "தொள்ளாயிரம்"];
const HUNDREDS_COMB: [&str; 10] =
    ["", "நூற்று", "இருநூற்று", "முந்நூற்று", "நானூற்று", "ஐநூற்று", "அறுநூற்று", "எழுநூற்று", "எண்நூற்று", "தொள்ளாயிரத்து"];

pub fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '௦'..='௯' => Some((c as u32 - '௦' as u32) as u8),
        _ => None,
    }
}

pub fn digitwise(s: &str) -> Option<String> {
    let mut bytes = 0;
    let mut count = 0usize;
    for c in s.chars() {
        bytes += DIGITS[digit(c)? as usize].len();
        count += 1;
    }
    let mut out = String::with_capacity(bytes + count.saturating_sub(1));
    for c in s.chars() {
        let name = DIGITS[digit(c)? as usize];
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(name);
    }
    Some(out)
}

fn write_prefix(out: &mut String, n: usize) {
    if n < 20 {
        out.push_str(PREFIX[n]);
        return;
    }
    let (t, u) = (n / 10, n % 10);
    if u == 0 {
        out.push_str(TENS[t]);
        if t != 9 {
            out.push('த');
        }
    } else {
        if t == 9 {
            out.push_str("தொண்ணூற்று ");
        } else {
            out.push_str(TENS[t]);
            out.push_str("த்து ");
        }
        out.push_str(PREFIX[u]);
    }
}

fn write_small(out: &mut String, n: usize) {
    if n < 10 {
        out.push_str(DIGITS[n]);
    } else if n < 100 {
        out.push_str(SMALL_NUMBERS[n - 10]);
    } else {
        let (head, rest) = (n / 100, n % 100);
        if rest == 0 {
            out.push_str(HUNDREDS[head]);
        } else {
            out.push_str(HUNDREDS_COMB[head]);
            out.push(' ');
            write_small(out, rest);
        }
    }
}

fn write_number(out: &mut String, n: u64) {
    if n < 1000 {
        write_small(out, n as usize);
        return;
    }
    if n < 100_000 {
        let (head, rest) = ((n / 1000) as usize, n % 1000);
        if head == 1 {
            out.push_str("ஆயிர");
        } else {
            write_prefix(out, head);
            out.push_str("ாயிர");
        }
        if rest == 0 {
            out.push_str("ம்");
        } else {
            out.push_str("த்து ");
            write_number(out, rest);
        }
        return;
    }
    let (scale, exact, combined) = if n < 10_000_000 {
        (100_000, "லட்சம்", "லட்சத்து")
    } else if n < 1_000_000_000 {
        (10_000_000, "கோடி", "கோடியே")
    } else if n < 100_000_000_000 {
        (1_000_000_000, "அரபம்", "அரபம்த்து")
    } else {
        (100_000_000_000, "கரபம்", "கரபம்")
    };
    let (head, rest) = (n / scale, n % scale);
    if head == 1 {
        out.push_str("ஒரு");
    } else {
        write_number(out, head);
    }
    out.push(' ');
    out.push_str(if rest == 0 || scale == 100_000_000_000 { exact } else { combined });
    if rest != 0 {
        out.push(' ');
        write_number(out, rest);
    }
}

fn spell(n: u64) -> String {
    let mut out = String::with_capacity(64);
    write_number(&mut out, n);
    out
}

/// Spell an ungrouped, unsigned string accepted by the cardinal graph.
pub fn cardinal(s: &str) -> Option<(String, bool)> {
    let mut chars = s.chars();
    let first = digit(chars.next()?)?;
    let mut n = first as u64;
    let mut len = 1usize;
    for c in chars {
        let d = digit(c)?;
        len += 1;
        if first != 0 && len <= 18 {
            n = n * 10 + d as u64;
        }
    }
    if len > 1 && first == 0 {
        return Some((digitwise(s)?, true));
    }
    if len > 18 {
        return None;
    }
    Some((spell(n), false))
}

/// Validate the exact cardinal graph without constructing spoken numbers.
/// The bool records the leading-zero path's 0.5 cost.
pub fn recognize(s: &str) -> Option<bool> {
    if !s.contains(',') {
        let mut chars = s.chars();
        let first = digit(chars.next()?)?;
        let mut len = 1usize;
        for c in chars {
            digit(c)?;
            len += 1;
        }
        return if first == 0 && len > 1 {
            Some(true)
        } else if len <= 18 {
            Some(false)
        } else {
            None
        };
    }
    let mut groups = s.split(',');
    let first = groups.next()?;
    let first_len = first.chars().count();
    if first_len == 0
        || first_len > 3
        || digit(first.chars().next()?)? == 0
        || !first.chars().all(|c| digit(c).is_some())
    {
        return None;
    }
    let mut western = true;
    let mut indian = first_len <= 2;
    let mut previous = 0usize;
    let mut count = 0usize;
    let mut digits = first_len;
    for group in groups {
        let n = group.chars().count();
        if n == 0 || !group.chars().all(|c| digit(c).is_some()) {
            return None;
        }
        if count > 0 && previous != 2 {
            indian = false;
        }
        western &= n == 3;
        previous = n;
        digits += n;
        count += 1;
    }
    indian &= previous == 3;
    (count > 0 && digits <= 18 && (western || indian)).then_some(false)
}
/// Append the final cardinal graph's reading to an existing tag buffer.
pub fn write_final(out: &mut String, s: &str) -> Option<()> {
    let leading = recognize(s)?;
    if leading {
        return write_digits(out, s);
    }
    let mut n = 0u64;
    for c in s.chars() {
        if c != ',' {
            n = n * 10 + digit(c)? as u64;
        }
    }
    write_number(out, n);
    Some(())
}

/// Append a digit-by-digit reading without creating an intermediate string.
pub fn write_digits(out: &mut String, s: &str) -> Option<()> {
    let mut first = true;
    for c in s.chars() {
        let word = DIGITS[digit(c)? as usize];
        if !first {
            out.push(' ');
        }
        first = false;
        out.push_str(word);
    }
    Some(())
}

pub fn attributive(s: &str) -> String {
    if let Some(prefix) = s.strip_suffix("பதினொன்று") {
        format!("{}பதினொரு", prefix)
    } else if let Some(prefix) = s.strip_suffix("ஒன்று") {
        format!("{}ஒரு", prefix)
    } else {
        s.to_owned()
    }
}

/// Cardinal graph including Indian and western comma groupings.
pub fn final_graph(s: &str) -> Option<(String, bool)> {
    if !s.contains(',') {
        return cardinal(s);
    }
    recognize(s)?;
    let digits: String = s.chars().filter(|&c| c != ',').collect();
    cardinal(&digits)
}

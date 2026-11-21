//! Dutch cardinal morphology from `nl/taggers/cardinal.py`.
//!
//! These functions describe the productive raw number graph; callers are responsible
//! for the weights and for wrapping the resulting words in a name field.

const DIGITS: [&str; 10] = ["nul", "een", "twee", "drie", "vier", "vijf", "zes", "zeven", "acht", "negen"];
const TEENS: [&str; 10] =
    ["tien", "elf", "twaalf", "dertien", "veertien", "vijftien", "zestien", "zeventien", "achttien", "negentien"];
const TENS: [&str; 10] =
    ["", "", "twintig", "dertig", "veertig", "vijftig", "zestig", "zeventig", "tachtig", "negentig"];
const PREFIX: [&str; 10] =
    ["", "eenen", "tweeën", "drieën", "vieren", "vijfen", "zesen", "zevenen", "achten", "negenen"];

pub(super) fn digit(c: char) -> Option<&'static str> {
    c.to_digit(10).filter(|_| c.is_ascii_digit()).map(|n| DIGITS[n as usize])
}

/// Append a canonical 0–999 cardinal directly to a winning tag.
pub(super) fn write_small(mut n: usize, out: &mut String) {
    if n >= 100 {
        let h = n / 100;
        if h != 1 {
            out.push_str(DIGITS[h]);
        }
        out.push_str("honderd");
        n %= 100;
        if n == 0 {
            return;
        }
    }
    match n {
        0..=9 => out.push_str(DIGITS[n]),
        10..=19 => out.push_str(TEENS[n - 10]),
        _ => {
            if n % 10 != 0 {
                out.push_str(PREFIX[n % 10]);
            }
            out.push_str(TENS[n / 10]);
        }
    }
}

fn under_hundred(n: usize) -> String {
    match n {
        0..=9 => DIGITS[n].to_owned(),
        10..=19 => TEENS[n - 10].to_owned(),
        _ if n % 10 == 0 => TENS[n / 10].to_owned(),
        _ => format!("{}{}", PREFIX[n % 10], TENS[n / 10]),
    }
}

fn under_thousand(n: usize) -> String {
    if n < 100 {
        return under_hundred(n);
    }
    let h = n / 100;
    let mut result = if h == 1 { "honderd".to_owned() } else { format!("{}honderd", DIGITS[h]) };
    if n % 100 != 0 {
        result.push_str(&under_hundred(n % 100));
    }
    result
}

fn under_million(n: usize) -> String {
    if n < 1000 {
        return under_thousand(n);
    }
    let thousands = n / 1000;
    let rem = n % 1000;
    // The source has both "duizend" and "eenduizend" paths for 1000.
    let mut result =
        if thousands == 1 { "duizend".to_owned() } else { format!("{}duizend", under_thousand(thousands)) };
    if rem != 0 {
        if thousands != 1 {
            result.push(' ');
        }
        result.push_str(&under_thousand(rem));
    }
    result
}

/// The ordinary cardinal reading of an ungrouped, canonical decimal integer.
/// The source graph accepts up to twelve digits and no leading zeros.
pub(super) fn raw(input: &str) -> Option<String> {
    if input.is_empty()
        || input.len() > 12
        || !input.bytes().all(|b| b.is_ascii_digit())
        || (input.len() > 1 && input.starts_with('0'))
    {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    if n < 1_000_000 {
        return Some(under_million(n as usize));
    }
    let mut rest = n;
    let mut words = Vec::new();
    for (scale, word) in [(1_000_000_000u64, "miljard"), (1_000_000u64, "miljoen")] {
        let coefficient = rest / scale;
        if coefficient != 0 {
            words.push(format!("{} {word}", under_thousand(coefficient as usize)));
            rest %= scale;
        }
    }
    if rest != 0 {
        words.push(under_million(rest as usize));
    }
    Some(words.join(" "))
}

/// Additional year-style path for 1100–1999. This is an alternative in the
/// cardinal graph, not a context-dependent year rewrite.
pub(super) fn year_alternative(input: &str) -> Option<String> {
    if input.len() != 4 || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let year: usize = input.parse().ok()?;
    if !(1100..=1999).contains(&year) {
        return None;
    }
    let mut spoken = format!("{}honderd", TEENS[year / 100 - 10]);
    let remainder = year % 100;
    if remainder != 0 {
        spoken.push_str(&under_hundred(remainder));
    }
    Some(spoken)
}

/// Cost-only form of the raw cardinal graph, in the same alternative order as
/// `paths`. A fixed stack buffer suffices: only the final thousand group has
/// several pronunciations.
pub(super) fn weights(input: &str) -> Option<([f64; 8], usize)> {
    if input.is_empty()
        || input.len() > 12
        || (input.len() > 1 && input.starts_with('0'))
        || !input.bytes().all(|b| b.is_ascii_digit())
    {
        return None;
    }
    fn hundred(n: u64) -> f64 {
        if (101..=109).contains(&n) { 0.01 } else { 0.0 }
    }
    fn number(n: u64) -> ([f64; 8], usize) {
        let mut out = [0.0; 8];
        let mut len = 0;
        let mut add = |w| {
            out[len] = w;
            len += 1;
        };
        if n < 1000 {
            add(hundred(n));
        } else if n < 1_000_000 {
            let high = n / 1000;
            let rem = n % 1000;
            if (1100..=1999).contains(&n) {
                add(0.0);
            }
            if high == 1 {
                if rem == 0 {
                    add(0.0);
                } else {
                    let w = hundred(rem) + if rem >= 100 { 0.00001 } else { 0.0 };
                    add(w);
                    add(w + 0.00001);
                }
            }
            add(hundred(high) + if high == 1 { 0.00001 } else { 0.0 } + if rem == 0 { 0.0 } else { hundred(rem) });
        } else {
            let scale = if n < 1_000_000_000 { 1_000_000 } else { 1_000_000_000 };
            let high = n / scale;
            let rem = n % scale;
            let base = hundred(high);
            if rem == 0 {
                add(base);
            } else {
                let (lower, count) = number(rem);
                for w in &lower[..count] {
                    add(base + w);
                }
            }
        }
        (out, len)
    }
    Some(number(input.parse().ok()?))
}

/// Enumerate the non-functional raw graph, including the weighted 101–109
/// deletion and both thousand/year pronunciations.
pub(super) fn paths(input: &str) -> Vec<(String, f64)> {
    if input.is_empty()
        || input.len() > 12
        || (input.len() > 1 && input.starts_with('0'))
        || !input.bytes().all(|b| b.is_ascii_digit())
    {
        return Vec::new();
    }
    fn small(s: &str, max: usize) -> Vec<(String, f64)> {
        if s == "0" {
            return vec![("nul".to_owned(), 0.0)];
        }
        if s.is_empty() || s.len() > max || s.starts_with('0') {
            return Vec::new();
        }
        let Ok(n) = s.parse::<usize>() else { return Vec::new() };
        if n < 1000 {
            return vec![(under_thousand(n), if (101..=109).contains(&n) { 0.01 } else { 0.0 })];
        }
        if max < 4 || n >= 1_000_000 {
            return Vec::new();
        }
        let high = n / 1000;
        let rem = n % 1000;
        let mut result = Vec::new();
        if let Some(y) = year_alternative(s) {
            result.push((y, 0.0));
        }
        if high == 1 {
            if rem == 0 {
                result.push(("duizend".to_owned(), 0.0));
            } else {
                for (r, rw) in small(&rem.to_string(), 3) {
                    let rw = rw + if rem >= 100 { 0.00001 } else { 0.0 };
                    result.push((format!("duizend{r}"), rw));
                    result.push((format!("duizend {r}"), rw + 0.00001));
                }
            }
        }
        for (h, hw) in small(&high.to_string(), 3) {
            let hw = hw + if high == 1 { 0.00001 } else { 0.0 };
            if rem == 0 {
                result.push((format!("{h}duizend"), hw));
            } else {
                for (r, rw) in small(&rem.to_string(), 3) {
                    result.push((format!("{h}duizend {r}"), hw + rw));
                }
            }
        }
        result
    }
    fn number(s: &str) -> Vec<(String, f64)> {
        if s.len() <= 6 {
            return small(s, 6);
        }
        let width = ((s.len() - 1) / 3) * 3;
        let unit = match width {
            6 => "miljoen",
            9 => "miljard",
            _ => return Vec::new(),
        };
        let (head, remainder) = s.split_at(s.len() - width);
        let remainder = remainder.trim_start_matches('0');
        let mut result = Vec::new();
        for (coefficient, cw) in small(head, 3) {
            if remainder.is_empty() {
                result.push((format!("{coefficient} {unit}"), cw));
            } else {
                for (lower, lw) in number(remainder) {
                    result.push((format!("{coefficient} {unit} {lower}"), cw + lw));
                }
            }
        }
        result
    }
    number(input)
}

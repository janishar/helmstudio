//! Swedish number morphology: cardinal, ordinal, fraction, and roman numbers.

const SMALL: [&str; 20] = [
    "noll", "ett", "två", "tre", "fyra", "fem", "sex", "sju", "åtta", "nio", "tio", "elva", "tolv", "tretton",
    "fjorton", "femton", "sexton", "sjutton", "arton", "nitton",
];

const TENS: [&str; 10] = ["", "", "tjugo", "trettio", "fyrtio", "femtio", "sextio", "sjuttio", "åttio", "nittio"];

const ORD_SMALL: [&str; 20] = [
    "nollte",
    "första",
    "andra",
    "tredje",
    "fjärde",
    "femte",
    "sjätte",
    "sjunde",
    "åttonde",
    "nionde",
    "tionde",
    "elfte",
    "tolfte",
    "trettonde",
    "fjortonde",
    "femtonde",
    "sextonde",
    "sjuttonde",
    "artonde",
    "nittonde",
];

const ORD_TENS: [&str; 10] =
    ["", "", "tjugonde", "trettionde", "fyrtionde", "femtionde", "sextionde", "sjuttionde", "åttionde", "nittionde"];

const MAGNITUDES: [&str; 6] = ["miljon", "miljard", "biljon", "biljard", "triljon", "triljard"];

/// Format a number under 100 into `out`.
pub fn under_hundred(out: &mut String, n: u8, common: bool) {
    if n < 20 {
        if n == 1 && common {
            out.push_str("en");
        } else {
            out.push_str(SMALL[n as usize]);
        }
    } else {
        out.push_str(TENS[(n / 10) as usize]);
        let rem = n % 10;
        if rem > 0 {
            if rem == 1 && common {
                out.push_str("en");
            } else {
                out.push_str(SMALL[rem as usize]);
            }
        }
    }
}

/// Format a number under 1000 into `out`.
pub fn under_thousand(out: &mut String, n: u16, common: bool) {
    let h = (n / 100) as usize;
    let rem = (n % 100) as u8;
    if h > 0 {
        if h == 1 {
            out.push_str("hundra");
        } else {
            out.push_str(SMALL[h]);
            out.push_str("hundra");
        }
    }
    if rem > 0 {
        under_hundred(out, rem, common);
    }
}

/// Format an ordinal under 100 into `out`.
pub fn ordinal_under_hundred(out: &mut String, n: u8) {
    if n < 20 {
        out.push_str(ORD_SMALL[n as usize]);
    } else {
        let rem = n % 10;
        if rem == 0 {
            out.push_str(ORD_TENS[(n / 10) as usize]);
        } else {
            out.push_str(TENS[(n / 10) as usize]);
            out.push_str(ORD_SMALL[rem as usize]);
        }
    }
}

/// Format an ordinal under 1000 into `out`.
pub fn ordinal_under_thousand(out: &mut String, n: u16) {
    let h = (n / 100) as usize;
    let rem = (n % 100) as u8;
    if h > 0 {
        if rem == 0 {
            if h == 1 {
                out.push_str("hundrade");
            } else {
                out.push_str(SMALL[h]);
                out.push_str("hundrade");
            }
            return;
        } else {
            if h == 1 {
                out.push_str("hundra");
            } else {
                out.push_str(SMALL[h]);
                out.push_str("hundra");
            }
        }
    }
    if rem > 0 {
        ordinal_under_hundred(out, rem);
    }
}

/// Format arbitrary non-negative integer string (up to 24 digits).
/// `counted`: whether a lone magnitude takes an article ("1 000 000" -> "en miljon" vs bare "miljon").
/// `common`: whether the final digit 1 agrees with common gender ("en") rather than neuter ("ett").
pub fn cardinal(s: &str, common: bool, counted: bool) -> Option<String> {
    let mut out = String::new();
    cardinal_into(&mut out, s, common, counted)?;
    Some(out)
}

pub(super) fn cardinal_into(out: &mut String, s: &str, common: bool, counted: bool) -> Option<()> {
    let digits = s.trim();
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let digits = digits.trim_start_matches('0');
    if digits.is_empty() {
        out.push_str("noll");
        return Some(());
    }
    if digits.len() > 24 {
        return None;
    }

    // Split into 3-digit groups from right
    let len = digits.len();
    let num_groups = (len + 2) / 3;
    let mut groups = [0u16; 8];
    let mut group_index = 0;
    let mut end = len;
    while end > 0 {
        let start = end.saturating_sub(3);
        let val: u16 = digits[start..end].parse().ok()?;
        groups[group_index] = val;
        group_index += 1;
        end = start;
    }

    let initial_len = out.len();
    let mut need_space = false;

    // Process from highest magnitude down to 0
    for idx in (0..num_groups).rev() {
        let g = groups[idx];
        if g == 0 {
            continue;
        }

        if need_space {
            out.push(' ');
            need_space = false;
        }

        if idx >= 2 {
            let mag_idx = idx - 2;
            if mag_idx >= MAGNITUDES.len() {
                return None;
            }
            let mag = MAGNITUDES[mag_idx];
            if g == 1 {
                if counted {
                    out.push_str("en ");
                }
                out.push_str(mag);
            } else {
                under_thousand(out, g, false);
                out.push(' ');
                out.push_str(mag);
                out.push_str("er");
            }
            need_space = true;
        } else if idx == 1 {
            // Thousands
            if g == 1 {
                out.push_str("tusen");
            } else {
                under_thousand(out, g, false);
                out.push_str("tusen");
            }
            need_space = true;
        } else {
            // Units (idx == 0)
            under_thousand(out, g, common);
        }
    }

    if out.len() == initial_len {
        out.push_str("noll");
    }
    Some(())
}

/// Format ordinal for a digit string (up to 24 digits).
pub fn ordinal(s: &str) -> Option<String> {
    let digits = s.trim();
    if digits.is_empty() || !digits.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let digits = digits.trim_start_matches('0');
    if digits.is_empty() {
        return Some("nollte".to_string());
    }
    if digits.len() > 24 {
        return None;
    }

    let len = digits.len();
    let num_groups = (len + 2) / 3;
    let mut groups = [0u16; 8];
    let mut group_index = 0;
    let mut end = len;
    while end > 0 {
        let start = end.saturating_sub(3);
        let val: u16 = digits[start..end].parse().ok()?;
        // Ordinal's fixed-width upper groups use the source's exposed,
        // unpadded cardinal component, plus its explicit "001" branch.
        if group_index > 0 && (2..100).contains(&val) {
            return None;
        }
        groups[group_index] = val;
        group_index += 1;
        end = start;
    }

    // Find the lowest non-zero group index
    let lowest_non_zero = groups[..num_groups].iter().position(|&g| g != 0)?;

    let mut out = String::new();
    let mut need_space = false;

    for idx in (0..num_groups).rev() {
        let g = groups[idx];
        if g == 0 {
            continue;
        }

        if need_space {
            out.push(' ');
            need_space = false;
        }

        let is_last = idx == lowest_non_zero;

        if idx >= 2 {
            let mag_idx = idx - 2;
            if mag_idx >= MAGNITUDES.len() {
                return None;
            }
            let mag = MAGNITUDES[mag_idx];
            if is_last {
                if g != 1 {
                    under_thousand(&mut out, g, false);
                    out.push(' ');
                }
                if let Some(stem) = mag.strip_suffix('d') {
                    out.push_str(stem);
                } else {
                    out.push_str(mag);
                }
                out.push_str("te");
            } else {
                if g == 1 {
                    out.push_str(mag);
                } else {
                    under_thousand(&mut out, g, false);
                    out.push(' ');
                    out.push_str(mag);
                    out.push_str("er");
                }
                need_space = true;
            }
        } else if idx == 1 {
            // Thousands
            if is_last {
                if g == 1 {
                    out.push_str("tusende");
                } else {
                    under_thousand(&mut out, g, false);
                    out.push_str("tusende");
                }
            } else {
                if g == 1 {
                    out.push_str("tusen");
                } else {
                    under_thousand(&mut out, g, false);
                    out.push_str("tusen");
                }
                need_space = true;
            }
        } else {
            // Units (idx == 0)
            ordinal_under_thousand(&mut out, g);
        }
    }

    Some(out)
}

/// Convert an ordinal string to its fractional denominator form.
pub fn ordinal_to_fraction_denominator(ord: &str) -> String {
    if ord == "andra" {
        return "halv".to_string();
    }
    if ord == "fjärde" {
        return "kvart".to_string();
    }

    const ENDINGS: &[(&str, &str)] = &[
        ("ljarte", "ljarddel"),
        ("tionde", "tiondel"),
        ("tonde", "tondel"),
        ("ljonte", "ljondel"),
        ("lliarte", "lliarddel"),
        ("llionte", "lliondel"),
        ("tusende", "tusendel"),
        ("drade", "dradel"),
        ("te", "tedel"),
        ("de", "dedel"),
        ("je", "jedel"),
        ("a", "adel"),
    ];

    for &(k, v) in ENDINGS {
        if ord.ends_with(k) {
            let base = &ord[..ord.len() - k.len()];
            let res = format!("{base}{v}");
            if res == "andradel" {
                return "halv".to_string();
            }
            if res == "fjärdedel" {
                return "kvart".to_string();
            }
            return res;
        }
    }

    format!("{ord}del")
}

/// Pluralize a fraction denominator ("kvart" -> "kvartar", "halv" -> "halva", "...del" -> "...delar").
pub fn pluralize_fraction_denominator(den: &str) -> String {
    if den == "kvart" {
        "kvartar".to_string()
    } else if den == "halv" {
        "halva".to_string()
    } else if let Some(base) = den.strip_suffix("del") {
        format!("{base}delar")
    } else {
        den.to_string()
    }
}

/// Parse Roman numeral into Arabic integer (1..2499).
pub fn roman_to_int(s: &str) -> Option<u32> {
    if !s.bytes().all(|c| matches!(c, b'I' | b'V' | b'X' | b'L' | b'C' | b'D' | b'M')) {
        return None;
    }
    if s.is_empty() {
        return None;
    }
    let mut total = 0u32;
    let mut prev = 0u32;
    for c in s.chars().rev() {
        let val = match c {
            'I' | 'i' => 1,
            'V' | 'v' => 5,
            'X' | 'x' => 10,
            'L' | 'l' => 50,
            'C' | 'c' => 100,
            'D' | 'd' => 500,
            'M' | 'm' => 1000,
            _ => return None,
        };
        if val < prev {
            total = total.checked_sub(val)?;
        } else {
            total = total.checked_add(val)?;
            prev = val;
        }
    }
    if total == 0 || total >= 2500 {
        return None;
    }
    let mut remaining = total;
    let mut rest = s;
    for (value, digits) in [
        (1000, "M"),
        (900, "CM"),
        (500, "D"),
        (400, "CD"),
        (100, "C"),
        (90, "XC"),
        (50, "L"),
        (40, "XL"),
        (10, "X"),
        (9, "IX"),
        (5, "V"),
        (4, "IV"),
        (1, "I"),
    ] {
        while remaining >= value {
            rest = rest.strip_prefix(digits)?;
            remaining -= value;
        }
    }
    rest.is_empty().then_some(total)
}

/// Single digits spelled out ("0" -> "noll", "1" -> "ett", ...).
pub fn spell_digit(d: u8) -> &'static str {
    match d {
        0 => "noll",
        1 => "ett",
        2 => "två",
        3 => "tre",
        4 => "fyra",
        5 => "fem",
        6 => "sex",
        7 => "sju",
        8 => "åtta",
        9 => "nio",
        _ => "",
    }
}

/// Spell out a run of digits digit by digit ("007" -> "noll noll sju").
pub fn spell_digits(digits: &str) -> String {
    let mut out = String::new();
    for (i, b) in digits.bytes().enumerate() {
        if i > 0 {
            out.push(' ');
        }
        if b.is_ascii_digit() {
            out.push_str(spell_digit(b - b'0'));
        }
    }
    out
}

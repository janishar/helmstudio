//! Malayalam cardinal morphology from `ml/taggers/cardinal.py`.
//! Number names are from Unicode CLDR release 49 (Unicode-3.0 license).

const DIGITS: [&str; 10] = ["പൂജ്യം", "ഒന്ന്", "രണ്ട്", "മൂന്ന്", "നാല്", "അഞ്ച്", "ആറ്", "ഏഴ്", "എട്ട്", "ഒൻപത്"];
const HUNDREDS: [&str; 9] = ["നൂറ്", "ഇരുനൂറ്", "മുന്നൂറ്", "നാന്നൂറ്", "അഞ്ഞൂറ്", "അറുന്നൂറ്", "എഴുന്നൂറ്", "എണ്ണൂറ്", "തൊള്ളായിരം"];
const HUNDREDS_COMBINING: [&str; 9] =
    ["നൂറ്റി", "ഇരുനൂറ്റി", "മുന്നൂറ്റി", "നാന്നൂറ്റി", "അഞ്ഞൂറ്റി", "അറുന്നൂറ്റി", "എഴുന്നൂറ്റി", "എണ്ണൂറ്റി", "തൊള്ളായിരത്തി"];
const THOUSANDS: [&str; 9] =
    ["ആയിരം", "രണ്ടായിരം", "മൂന്നായിരം", "നാലായിരം", "അഞ്ചായിരം", "ആറായിരം", "ഏഴായിരം", "എട്ടായിരം", "ഒൻപതിനായിരം"];
const THOUSANDS_COMBINING: [&str; 9] =
    ["ആയിരത്തി", "രണ്ടായിരത്തി", "മൂന്നായിരത്തി", "നാലായിരത്തി", "അഞ്ചായിരത്തി", "ആറായിരത്തി", "ഏഴായിരത്തി", "എട്ടായിരത്തി", "ഒൻപതിനായിരത്തി"];
crate::tsv!(TWO_DIGITS, "ml/numbers/teens_and_ties.tsv");
static SMALL: std::sync::LazyLock<[&'static str; 90]> = std::sync::LazyLock::new(|| {
    let mut values = [""; 90];
    for row in TWO_DIGITS.rows() {
        if let Ok(n) = row.key.parse::<usize>() {
            if (10..=99).contains(&n) {
                values[n - 10] = &row.value;
            }
        }
    }
    assert!(values.iter().all(|value| !value.is_empty()), "Malayalam two-digit table is complete");
    values
});

pub fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '൦'..='൯' => Some((c as u32 - '൦' as u32) as u8),
        _ => None,
    }
}

pub fn digitwise(input: &str) -> Option<String> {
    let mut out = String::new();
    for c in input.chars() {
        if !out.is_empty() {
            out.push(' ');
        }
        out.push_str(DIGITS[digit(c)? as usize]);
    }
    if out.is_empty() { None } else { Some(out) }
}

fn under_hundred(n: u64) -> String {
    if n < 10 {
        return DIGITS[n as usize].to_owned();
    }
    SMALL[(n - 10) as usize].to_owned()
}

fn cardinal_number(n: u64) -> String {
    if n < 100 {
        return under_hundred(n);
    }
    if n < 1000 {
        let h = (n / 100 - 1) as usize;
        let rest = n % 100;
        return if rest == 0 {
            HUNDREDS[h].to_owned()
        } else {
            format!("{} {}", HUNDREDS_COMBINING[h], cardinal_number(rest))
        };
    }
    if n < 100_000 {
        let h = n / 1000;
        let rest = n % 1000;
        let prefix = if h < 10 {
            if rest == 0 {
                THOUSANDS[h as usize - 1].to_owned()
            } else {
                THOUSANDS_COMBINING[h as usize - 1].to_owned()
            }
        } else {
            format!("{} {}", under_hundred(h), if rest == 0 { "ആയിരം" } else { "ആയിരത്തി" })
        };
        return if rest == 0 { prefix } else { format!("{} {}", prefix, cardinal_number(rest)) };
    }
    let (scale, exact, combining) = if n < 10_000_000 {
        (100_000, "ലക്ഷം", "ലക്ഷത്തി")
    } else if n < 1_000_000_000 {
        (10_000_000, "കോടി", "കോടി")
    } else if n < 100_000_000_000 {
        (1_000_000_000, "അറബ്", "അറബ്")
    } else {
        (100_000_000_000, "ഖറബ്", "ഖറബ്")
    };
    let head = n / scale;
    let rest = n % scale;
    let head = if head == 1 { "ഒരു".to_owned() } else { cardinal_number(head) };
    let prefix = format!("{} {}", head, if rest == 0 { exact } else { combining });
    if rest == 0 { prefix } else { format!("{} {}", prefix, cardinal_number(rest)) }
}

/// The ungrouped `graph_without_leading_zeros`, including Malayalam digits.
pub fn cardinal(input: &str) -> Option<String> {
    let mut n = 0u64;
    let mut count = 0;
    let mut first = 0;
    for c in input.chars() {
        let d = digit(c)?;
        if count == 0 {
            first = d;
        }
        count += 1;
        if count > 18 {
            return None;
        }
        n = n.checked_mul(10)?.checked_add(d as u64)?;
    }
    if count == 0 || (count > 1 && first == 0) {
        return None;
    }
    Some(cardinal_number(n))
}

/// Cardinal `final_graph`; leading-zero digitwise paths carry an extra 0.5.
pub fn final_cardinal(input: &str) -> Option<(String, i64)> {
    if input.contains(',') {
        let mut groups = input.split(',');
        let first = groups.next()?;
        let first_len = first.chars().count();
        if first_len == 0 || !first.chars().all(|c| digit(c).is_some()) {
            return None;
        }
        let mut western = (1..=3).contains(&first_len);
        let mut indian = (1..=2).contains(&first_len);
        let mut previous = None;
        for group in groups {
            let len = group.chars().count();
            if len == 0 || !group.chars().all(|c| digit(c).is_some()) {
                return None;
            }
            western &= len == 3;
            if let Some(width) = previous {
                indian &= width == 2;
            }
            previous = Some(len);
        }
        indian &= previous == Some(3);
        if previous.is_none() || !(western || indian) {
            return None;
        }
        let plain: String = input.chars().filter(|&c| c != ',').collect();
        return cardinal(&plain).map(|s| (s, 0));
    }
    let mut chars = input.chars();
    let first = chars.next()?;
    if digit(first) == Some(0) && chars.next().is_some() {
        return if input.chars().all(|c| digit(c).is_some()) { digitwise(input).map(|s| (s, 500_000)) } else { None };
    }
    cardinal(input).map(|s| (s, 0))
}

pub fn attributive(input: &str) -> Option<(String, i64)> {
    let (mut word, weight) = final_cardinal(input)?;
    if word == "ഒന്ന്" {
        word = "ഒരു".to_owned();
    } else if let Some(prefix) = word.strip_suffix(" ഒന്ന്") {
        word = format!("{prefix} ഒരു");
    }
    Some((word, weight))
}

pub fn ascii_digits(input: &str) -> Option<String> {
    input.chars().map(|c| digit(c).map(|d| (b'0' + d) as char)).collect()
}

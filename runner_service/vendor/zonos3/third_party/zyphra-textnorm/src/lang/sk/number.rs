// Slovak cardinal morphology, matching taggers/cardinal.py.
const DIGITS: [&str; 10] = ["nula", "jeden", "dva", "tri", "štyri", "päť", "šesť", "sedem", "osem", "deväť"];
const TEENS: [&str; 10] =
    ["desať", "jedenásť", "dvanásť", "trinásť", "štrnásť", "pätnásť", "šestnásť", "sedemnásť", "osemnásť", "devätnásť"];
const TENS: [&str; 10] =
    ["", "", "dvadsať", "tridsať", "štyridsať", "päťdesiat", "šesťdesiat", "sedemdesiat", "osemdesiat", "deväťdesiat"];
const HUNDREDS: [&str; 10] =
    ["", "sto", "dvesto", "tristo", "štyristo", "päťsto", "šesťsto", "sedemsto", "osemsto", "deväťsto"];
/// Validate grouped ASCII cardinal input without allocating a split vector.
pub(super) fn grouped(input: &str, separators: &[char]) -> Option<String> {
    let mut digits = [0u8; 12];
    let mut len = 0;
    let mut group = 0;
    let mut groups = 0;
    for c in input.chars() {
        if c.is_ascii_digit() {
            if len == digits.len() {
                return None;
            }
            digits[len] = c as u8;
            len += 1;
            group += 1;
        } else if separators.contains(&c) {
            if group == 0 || if groups == 0 { group > 3 } else { group != 3 } {
                return None;
            }
            groups += 1;
            group = 0;
        } else {
            return None;
        }
    }
    if groups == 0 || group != 3 {
        return None;
    }
    positive(std::str::from_utf8(&digits[..len]).ok()?)
}

fn triad(n: usize) -> String {
    let mut out = String::new();
    if n >= 100 {
        out.push_str(HUNDREDS[n / 100]);
    }
    let rest = n % 100;
    if rest != 0 && n >= 100 {
        out.push(' ');
    }
    match rest {
        0 => {}
        1..=9 => out.push_str(DIGITS[rest]),
        10..=19 => out.push_str(TEENS[rest - 10]),
        _ => {
            out.push_str(TENS[rest / 10]);
            if rest % 10 != 0 {
                out.push_str(DIGITS[rest % 10]);
            }
        }
    }
    out
}

fn thousand(n: usize) -> String {
    match n {
        1 => "tisíc".into(),
        2 => "dvetisíc".into(),
        3..=9 => format!("{}tisíc", DIGITS[n]),
        10..=99 if n < 20 || n % 10 == 0 => format!("{}tisíc", triad(n)),
        _ => format!("{} tisíc", triad(n)),
    }
}

/// Transduce an ungrouped, positive ASCII cardinal accepted by `all_positive`.
pub(super) fn positive(input: &str) -> Option<String> {
    if input == "0" {
        return Some(if super::building() { "nula".into() } else { String::new() });
    }
    if input.is_empty() || input.len() > 12 || input.starts_with('0') || !input.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    if !super::building() {
        return Some(String::new());
    }
    let mut n: u64 = input.parse().ok()?;
    let billions = (n / 1_000_000_000) as usize;
    n %= 1_000_000_000;
    let millions = (n / 1_000_000) as usize;
    n %= 1_000_000;
    let thousands = (n / 1000) as usize;
    let units = (n % 1000) as usize;
    let mut parts = Vec::new();
    if billions != 0 {
        parts.push(match billions {
            1 => "jedna miliarda".into(),
            2 => "dve miliardy".into(),
            3..=4 => format!("{} miliardy", DIGITS[billions]),
            _ => format!("{} miliárd", triad(billions)),
        });
    }
    if millions != 0 {
        parts.push(match millions {
            1 => "jeden milión".into(),
            2..=4 => format!("{} milióny", DIGITS[millions]),
            _ => format!("{} miliónov", triad(millions)),
        });
    }
    if thousands != 0 {
        parts.push(thousand(thousands));
    }
    if units != 0 {
        parts.push(triad(units));
    }
    Some(parts.join(" "))
}

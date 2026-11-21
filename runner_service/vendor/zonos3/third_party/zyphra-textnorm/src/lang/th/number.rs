//! Thai cardinal primitives corresponding to `th/taggers/cardinal.py`.

const DIGITS: [&str; 10] = ["ศูนย์", "หนึ่ง", "สอง", "สาม", "สี่", "ห้า", "หก", "เจ็ด", "แปด", "เก้า"];

pub(crate) fn digit(c: char) -> Option<u8> {
    match c {
        '0'..='9' => Some(c as u8 - b'0'),
        '๐'..='๙' => Some((c as u32 - '๐' as u32) as u8),
        _ => None,
    }
}

pub(crate) fn digit_words(input: &str) -> Option<String> {
    let mut out = String::new();
    for c in input.chars() {
        out.push_str(DIGITS[digit(c)? as usize]);
    }
    if out.is_empty() { None } else { Some(out) }
}

fn chunk(mut n: u32, final_one: bool, out: &mut String) {
    let start = out.len();
    for (place, suffix) in [(100_000, "แสน"), (10_000, "หมื่น"), (1_000, "พัน"), (100, "ร้อย")]
    {
        let d = n / place;
        if d != 0 {
            out.push_str(DIGITS[d as usize]);
            out.push_str(suffix);
        }
        n %= place;
    }
    let tens = n / 10;
    match tens {
        0 => (),
        1 => out.push_str("สิบ"),
        2 => out.push_str("ยี่สิบ"),
        _ => {
            out.push_str(DIGITS[tens as usize]);
            out.push_str("สิบ");
        }
    }
    let ones = n % 10;
    if ones != 0 {
        if ones == 1 && (tens != 0 || out.len() != start || final_one) {
            out.push_str("เอ็ด");
        } else {
            out.push_str(DIGITS[ones as usize]);
        }
    }
}

/// The source prefilter removes commas after the first digit without validating grouping.
pub(crate) fn cardinal(input: &str) -> Option<String> {
    let mut n: u64 = 0;
    let mut count = 0;
    let mut first = None;
    for c in input.chars() {
        if c == ',' && count != 0 {
            continue;
        }
        let d = digit(c)?;
        if first.is_none() {
            first = Some(d);
        }
        count += 1;
        if count > 18 {
            return None;
        }
        // At most eighteen decimal digits are accepted, so this fits in u64.
        n = n * 10 + u64::from(d);
    }
    if count == 0 || (count > 1 && first == Some(0)) {
        return None;
    }
    if n == 0 {
        return Some(DIGITS[0].to_owned());
    }
    let mut out = String::with_capacity(count * 24);
    let trillions = n / 1_000_000_000_000;
    let millions = n / 1_000_000 % 1_000_000;
    let units = n % 1_000_000;
    if trillions != 0 {
        chunk(trillions as u32, false, &mut out);
        out.push_str("ล้านล้าน");
    }
    if millions != 0 {
        chunk(millions as u32, false, &mut out);
        out.push_str("ล้าน");
    }
    if units != 0 {
        let final_one = !out.is_empty();
        chunk(units as u32, final_one, &mut out);
    }
    Some(out)
}

pub(crate) fn positive(input: &str) -> Option<String> {
    let out = cardinal(input)?;
    (out != "ศูนย์").then_some(out)
}

/// Incremental version of `valid_number`, for the union's successive spans.
pub(crate) struct NumberPrefix {
    count: usize,
    first: Option<u8>,
    negative: bool,
    all_digits: bool,
    viable: bool,
}
impl NumberPrefix {
    pub(crate) fn new(negative: bool) -> Self {
        Self { count: 0, first: None, negative, all_digits: true, viable: true }
    }
    pub(crate) fn advance(&mut self, c: char, is_first: bool) {
        if !self.viable {
            return;
        }
        if is_first && self.negative && matches!(c, '-' | '−') {
            return;
        }
        if c == ',' && self.count != 0 {
            self.all_digits = false;
            return;
        }
        let Some(d) = digit(c) else {
            self.viable = false;
            return;
        };
        self.first.get_or_insert(d);
        self.count += 1;
        if self.count > 18 && (self.negative || self.first != Some(0) || !self.all_digits) {
            self.viable = false;
        }
    }
    pub(crate) fn accepted(&self) -> bool {
        self.viable
            && self.count > 0
            && ((self.count <= 18 && (self.count == 1 || self.first != Some(0)))
                || (!self.negative && self.count > 1 && self.first == Some(0) && self.all_digits))
    }
}

/// Validate the cardinal and leading-zero serial paths without spelling them.
pub(crate) fn valid_number(input: &str) -> bool {
    let (negative, s) = match input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        Some(s) => (true, s),
        None => (false, input),
    };
    let mut count = 0;
    let mut first = None;
    let mut all_digits = true;
    for c in s.chars() {
        if c == ',' && count != 0 {
            all_digits = false;
            continue;
        }
        let Some(d) = digit(c) else { return false };
        first.get_or_insert(d);
        count += 1;
        if count > 18 && (negative || first != Some(0) || !all_digits) {
            return false;
        }
    }
    count > 0
        && ((count <= 18 && (count == 1 || first != Some(0)))
            || (!negative && count > 1 && first == Some(0) && all_digits))
}

pub(crate) fn valid_positive(s: &str) -> bool {
    s.chars().next().and_then(digit).is_some_and(|d| d != 0) && valid_number(s)
}

pub(crate) fn number(input: &str) -> Option<String> {
    if let Some(rest) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        return Some(format!("ลบ{}", cardinal(rest)?));
    }
    cardinal(input).or_else(|| {
        if input.chars().count() > 1 && input.chars().next().is_some_and(|c| c == '0' || c == '๐') {
            digit_words(input)
        } else {
            None
        }
    })
}

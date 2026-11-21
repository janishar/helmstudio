use super::number::cardinal;

fn positive_number(s: &str) -> Option<String> {
    let n = cardinal(s)?;
    (n != "ศูนย์").then_some(n)
}

fn vulgar(c: char) -> Option<&'static str> {
    Some(match c {
        '½' => "เศษหนึ่งส่วนสอง",
        '¼' => "เศษหนึ่งส่วนสี่",
        '¾' => "เศษสามส่วนสี่",
        '⅓' => "เศษหนึ่งส่วนสาม",
        '⅔' => "เศษสองส่วนสาม",
        '⅕' => "เศษหนึ่งส่วนห้า",
        '⅖' => "เศษสองส่วนห้า",
        '⅗' => "เศษสามส่วนห้า",
        '⅘' => "เศษสี่ส่วนห้า",
        '⅙' => "เศษหนึ่งส่วนหก",
        '⅚' => "เศษห้าส่วนหก",
        '⅛' => "เศษหนึ่งส่วนแปด",
        '⅜' => "เศษสามส่วนแปด",
        '⅝' => "เศษห้าส่วนแปด",
        '⅞' => "เศษเจ็ดส่วนแปด",
        '⅐' => "เศษหนึ่งส่วนเจ็ด",
        '⅑' => "เศษหนึ่งส่วนเก้า",
        '⅒' => "เศษหนึ่งส่วนสิบ",
        _ => return None,
    })
}

pub(crate) fn fraction(input: &str) -> Option<String> {
    let (sign, s) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        ("ลบ", s)
    } else {
        ("", input)
    };
    if let Some(c) = s.chars().last() {
        if let Some(v) = vulgar(c) {
            let prefix = &s[..s.len() - c.len_utf8()];
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            let whole = if prefix.is_empty() { String::new() } else { positive_number(prefix)? };
            return Some(format!("{sign}{whole}{v}"));
        }
    }
    let (left, right) = s.split_once('/')?;
    let denominator = positive_number(right)?;
    let (whole, numerator) = match left.split_once(' ') {
        Some((a, b)) => (positive_number(a)?, positive_number(b)?),
        None => (String::new(), positive_number(left)?),
    };
    Some(format!("{sign}{whole}เศษ{numerator}ส่วน{denominator}"))
}

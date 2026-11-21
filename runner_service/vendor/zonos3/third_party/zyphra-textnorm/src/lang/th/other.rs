use super::number;

pub(crate) fn ordinal(s: &str) -> Option<String> {
    if let Some(x) = s.strip_prefix("ที่") {
        let x = x.strip_prefix(' ').unwrap_or(x);
        return Some(format!("ที่{}", number::positive(x)?));
    }
    for suffix in ["st", "nd", "rd", "th"] {
        if let Some(x) = s.strip_suffix(suffix) {
            if let Some(x) = number::positive(x) {
                return Some(format!("ที่{x}"));
            }
        }
    }
    None
}
pub(crate) fn roman(s: &str) -> Option<String> {
    const ROMANS: &[&str] = &[
        "I", "II", "III", "IV", "V", "VI", "VII", "VIII", "IX", "X", "XI", "XII", "XIII", "XIV", "XV", "XVI", "XVII",
        "XVIII", "XIX", "XX", "XXI", "XXII",
    ];
    for (p, w) in [
        ("Chapter", "บทที่"),
        ("chapter", "บทที่"),
        ("บทที่", "บทที่"),
        ("Part", "ภาคที่"),
        ("part", "ภาคที่"),
        ("ภาคที่", "ภาคที่"),
        ("Vol.", "เล่มที่"),
        ("vol.", "เล่มที่"),
        ("เล่มที่", "เล่มที่"),
        ("Louis ", "หลุยส์ที่"),
        ("Elizabeth ", "เอลิซาเบธที่"),
        ("Rama ", "รัชกาลที่"),
        ("Henry ", "เฮนรีที่"),
        ("George ", "จอร์จที่"),
        ("Charles ", "ชาร์ลส์ที่"),
        ("รัชกาลที่", "รัชกาลที่"),
        ("century ", "ศตวรรษที่"),
        ("Century ", "ศตวรรษที่"),
        ("ศตวรรษที่", "ศตวรรษที่"),
    ] {
        if let Some(x) = s.strip_prefix(p) {
            let x = x.strip_prefix(' ').unwrap_or(x);
            let x = x.strip_prefix(' ').unwrap_or(x);
            if let Some(i) = ROMANS.iter().position(|r| *r == x) {
                return Some(format!("{w}{}", number::positive(&(i + 1).to_string())?));
            }
        }
    }
    None
}
pub(crate) fn telephone(s: &str) -> Vec<String> {
    let mut out = Vec::new();
    if let Some(rest) = s.strip_prefix('+') {
        if rest.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
            let digits: Vec<char> = rest.chars().filter(|c| c.is_ascii_digit()).collect();
            if (7..=15).contains(&digits.len()) {
                let words: Vec<String> = digits.iter().filter_map(|c| number::digit_words(&c.to_string())).collect();
                out.push(format!("บวก {}", words.join(" ")));
            }
        }
    }
    let parts: Vec<&str> = s.split(|c| c == ' ' || c == '-').collect();
    let lengths: Vec<usize> = parts.iter().map(|p| p.chars().count()).collect();
    if matches!(lengths.as_slice(), [3, 3, 4] | [2, 3, 4] | [3, 4, 4] | [3, 8]) {
        let words: Option<Vec<String>> = parts.iter().map(|x| number::digit_words(x)).collect();
        if let Some(words) = words {
            out.push(words.join(" "));
        }
    }
    out
}

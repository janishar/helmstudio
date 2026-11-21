//! Roman numeral classifier and mapping for Urdu text normalization.

use super::Sink;
use crate::text::cost;
use std::collections::{HashMap, HashSet};
use std::sync::LazyLock;

static ROMANS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| ROMAN_TABLE.iter().copied().collect());
static KEYS: LazyLock<HashMap<&'static str, &'static str>> = LazyLock::new(|| KEY_PHRASES.iter().copied().collect());
static KEYS_BY_FIRST: LazyLock<HashMap<char, Vec<(&'static str, &'static str)>>> = LazyLock::new(|| {
    let mut index: HashMap<char, Vec<(&'static str, &'static str)>> = HashMap::new();
    for &(key, value) in KEY_PHRASES {
        if let Some(c) = key.chars().next() {
            index.entry(c).or_default().push((key, value));
        }
    }
    index
});
static URDU_KEYS: LazyLock<HashSet<&'static str>> = LazyLock::new(|| URDU_PHRASES.iter().copied().collect());
static EXCEPTIONS: LazyLock<HashMap<&'static str, (&'static str, &'static str)>> =
    LazyLock::new(|| ORDINAL_EXCEPTIONS.iter().map(|&(fused, numeral, spoken)| (fused, (numeral, spoken))).collect());

pub const ROMAN_TABLE: &[(&str, &str)] = &[
    ("C", "ایک سو"),
    ("XCIX", "ننانوے"),
    ("XCVIII", "اٹھانوے"),
    ("XCVII", "ستانوے"),
    ("XCVI", "چھیانوے"),
    ("XCV", "پچانوے"),
    ("XCIV", "چورانوے"),
    ("XCIII", "ترانوے"),
    ("XCII", "بانوے"),
    ("XCI", "اکیانوے"),
    ("XC", "نوے"),
    ("LXXXIX", "نواسی"),
    ("LXXXVIII", "اٹھاسی"),
    ("LXXXVII", "ستاسی"),
    ("LXXXVI", "چھیاسی"),
    ("LXXXV", "پچاسی"),
    ("LXXXIV", "چوراسی"),
    ("LXXXIII", "تراسی"),
    ("LXXXII", "بیاسی"),
    ("LXXXI", "اکیاسی"),
    ("LXXX", "اسی"),
    ("LXXIX", "انیاسی"),
    ("LXXVIII", "اٹھہتر"),
    ("LXXVII", "ستہتر"),
    ("LXXVI", "چھہتر"),
    ("LXXV", "پچہتر"),
    ("LXXIV", "چوہتر"),
    ("LXXIII", "تہتر"),
    ("LXXII", "بہتر"),
    ("LXXI", "اکہتر"),
    ("LXX", "ستر"),
    ("LXIX", "انہتر"),
    ("LXVIII", "اڑسٹھ"),
    ("LXVII", "سڑسٹھ"),
    ("LXVI", "چھیاسٹھ"),
    ("LXV", "پینسٹھ"),
    ("LXIV", "چونسٹھ"),
    ("LXIII", "تریسٹھ"),
    ("LXII", "باسٹھ"),
    ("LXI", "اکسٹھ"),
    ("LX", "ساٹھ"),
    ("LIX", "انسٹھ"),
    ("LVIII", "اٹھاون"),
    ("LVII", "ستاون"),
    ("LVI", "چھپن"),
    ("LV", "پچپن"),
    ("LIV", "چون"),
    ("LIII", "تریپن"),
    ("LII", "باون"),
    ("LI", "اکیاون"),
    ("L", "پچاس"),
    ("XLIX", "انچاس"),
    ("XLVIII", "اڑتالیس"),
    ("XLVII", "سینتالیس"),
    ("XLVI", "چھیالیس"),
    ("XLV", "پینتالیس"),
    ("XLIV", "چوالیس"),
    ("XLIII", "تینتالیس"),
    ("XLII", "بیالیس"),
    ("XLI", "اکتالیس"),
    ("XL", "چالیس"),
    ("XXXIX", "انتالیس"),
    ("XXXVIII", "اڑتیس"),
    ("XXXVII", "سینتیس"),
    ("XXXVI", "چھتیس"),
    ("XXXV", "پینتیس"),
    ("XXXIV", "چونتیس"),
    ("XXXIII", "تینتیس"),
    ("XXXII", "بتیس"),
    ("XXXI", "اکتیس"),
    ("XXX", "تیس"),
    ("XXIX", "انتیس"),
    ("XXVIII", "اٹھائیس"),
    ("XXVII", "ستائیس"),
    ("XXVI", "چھبیس"),
    ("XXV", "پچیس"),
    ("XXIV", "چوبیس"),
    ("XXIII", "تئیس"),
    ("XXII", "بائیس"),
    ("XXI", "اکیس"),
    ("XX", "بیس"),
    ("XIX", "انیس"),
    ("XVIII", "اٹھارہ"),
    ("XVII", "سترہ"),
    ("XVI", "سولہ"),
    ("XV", "پندرہ"),
    ("XIV", "چودہ"),
    ("XIII", "تیرہ"),
    ("XII", "بارہ"),
    ("XI", "گیارہ"),
    ("X", "دس"),
    ("IX", "نو"),
    ("VIII", "آٹھ"),
    ("VII", "سات"),
    ("VI", "چھ"),
    ("V", "پانچ"),
    ("IV", "چار"),
    ("III", "تین"),
    ("II", "دو"),
    ("I", "ایک"),
];

pub fn spoken(roman: &str) -> Option<&'static str> {
    ROMANS.get(roman).copied()
}

pub const KEY_PHRASES: &[(&str, &str)] = &[
    ("جنگ عظیم", "جنگ عظیم"),
    ("World War", "جنگ عظیم"),
    ("Chapter", "باب"),
    ("chapter", "باب"),
    ("Ch.", "باب"),
    ("Section", "دفعہ"),
    ("section", "دفعہ"),
    ("Volume", "جلد"),
    ("volume", "جلد"),
    ("Vol.", "جلد"),
    ("Phase", "مرحلہ"),
    ("phase", "مرحلہ"),
    ("Part", "حصہ"),
    ("part", "حصہ"),
    ("Book", "کتاب"),
    ("Act", "ایکٹ"),
    ("مرحلہ", "مرحلہ"),
    ("جماعت", "جماعت"),
    ("صفحہ", "صفحہ"),
    ("کتاب", "کتاب"),
    ("ایکٹ", "ایکٹ"),
    ("منظر", "منظر"),
    ("دفعہ", "دفعہ"),
    ("حصہ", "حصہ"),
    ("جلد", "جلد"),
    ("صدی", "صدی"),
    ("باب", "باب"),
    ("شق", "شق"),
];

pub const URDU_PHRASES: &[&str] =
    &["جنگ عظیم", "مرحلہ", "جماعت", "صفحہ", "کتاب", "ایکٹ", "منظر", "دفعہ", "حصہ", "جلد", "صدی", "باب", "شق"];

pub const ORDINAL_EXCEPTIONS: &[(&str, &str, &str)] = &[
    ("IVواں", "IV", "چوتھا"),
    ("IVویں", "IV", "چوتھے"),
    ("IIIواں", "III", "تیسرا"),
    ("IIIویں", "III", "تیسرے"),
    ("IIواں", "II", "دوسرا"),
    ("IIویں", "II", "دوسرے"),
    ("VIواں", "VI", "چھٹا"),
    ("VIویں", "VI", "چھٹے"),
    ("IXواں", "IX", "نواں"),
    ("IXویں", "IX", "نویں"),
    ("Iواں", "I", "پہلا"),
    ("Iویں", "I", "پہلے"),
];

pub(crate) fn possible_start(text: &[char]) -> bool {
    let Some(&first) = text.first() else { return false };
    if matches!(first, 'I' | 'V' | 'X' | 'L' | 'C') {
        return true;
    }
    KEYS_BY_FIRST.get(&first).is_some_and(|keys| {
        keys.iter().any(|(key, _)| {
            let len = key.chars().count();
            text.len() > len && text[..len].iter().copied().eq(key.chars()) && matches!(text[len], ' ' | '-')
        })
    })
}

pub fn classify(input: &str, end: usize, out: &mut Sink) {
    // Keep source order for overlapping key prefixes.
    if let Some(keys) = input.chars().next().and_then(|c| KEYS_BY_FIRST.get(&c)) {
        for &(key_input, urdu_norm) in keys {
            let Some(rest) = input.strip_prefix(key_input) else { continue };
            let Some(rem) = rest.strip_prefix('-').or_else(|| rest.strip_prefix(' ')) else { continue };
            if ROMANS.contains_key(rem) {
                out.push(end, cost(1.1), 12, || {
                    let key = urdu_norm.replace(' ', "\u{a0}");
                    format!("preserve_order: true key_cardinal: \"{}\" integer: \"{}\"", key, rem)
                });
            }
        }
    }

    // Roman keys are ASCII and contain neither separator nor ordinal suffix.
    let numeral_end = input.bytes().take_while(|b| b.is_ascii_uppercase()).count();
    if numeral_end == 0 {
        return;
    }
    let numeral = &input[..numeral_end];
    let Some(spoken_card) = ROMANS.get(numeral).copied() else { return };
    let rest = &input[numeral_end..];
    if let Some(key) = rest.strip_prefix('-').or_else(|| rest.strip_prefix(' ')) {
        if let Some(urdu_norm) = KEYS.get(key) {
            out.push(end, cost(1.1), 12, || {
                let key = urdu_norm.replace(' ', "\u{a0}");
                format!("preserve_order: true integer: \"{}\" key_cardinal: \"{}\"", numeral, key)
            });
        }
    }

    for suffix in ["واں", "ویں"] {
        let Some(after) = rest.strip_prefix(suffix) else { continue };
        let key = if after.is_empty() {
            None
        } else if let Some(key) = after.strip_prefix(' ').filter(|key| URDU_KEYS.contains(key)) {
            Some(key)
        } else {
            continue;
        };
        let fused = &input[..numeral_end + suffix.len()];
        if let Some(&(num, spoken_ord)) = EXCEPTIONS.get(fused) {
            out.push(end, cost(1.0), 12, || {
                if let Some(key) = key {
                    let key = key.replace(' ', "\u{a0}");
                    format!(
                        "preserve_order: true integer: \"{}\" default_ordinal: \"{}\" key_cardinal: \"{}\"",
                        num, spoken_ord, key
                    )
                } else {
                    format!("preserve_order: true integer: \"{}\" default_ordinal: \"{}\"", num, spoken_ord)
                }
            });
        } else {
            out.push(end, cost(1.1), 12, || {
                if let Some(key) = key {
                    let key = key.replace(' ', "\u{a0}");
                    format!(
                        "preserve_order: true integer: \"{}\" default_ordinal: \"{}{}\" key_cardinal: \"{}\"",
                        numeral, spoken_card, suffix, key
                    )
                } else {
                    format!(
                        "preserve_order: true integer: \"{}\" default_ordinal: \"{}{}\"",
                        numeral, spoken_card, suffix
                    )
                }
            });
        }
    }
}

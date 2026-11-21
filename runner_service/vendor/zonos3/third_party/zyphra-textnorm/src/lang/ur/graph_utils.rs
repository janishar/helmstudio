//! Urdu shared graph constants and helpers.

use crate::text::{Cost, cost};

pub const UR_DEDH: &str = "ڈیڑھ";
pub const UR_DHAI: &str = "ڈھائی";
pub const UR_SAVA: &str = "سوا";
pub const UR_SADHE: &str = "ساڑھے";
pub const UR_PAUNE: &str = "پونے";
pub const UR_BY: &str = "بائی";
pub const MINUS: &str = "منفی ";

pub const MIN_NEG_WEIGHT: Cost = cost(-0.0001);
pub const MIN_POS_WEIGHT: Cost = cost(0.0001);

/// Prefix modifier words applied before semiotic classes in `tokenize_and_classify.py`.
pub const PREFIX_MODS: &[(&str, &str)] =
    &[("§§", "دفعات"), ("§", "دفعہ"), ("~", "تقریباً"), ("≈", "تقریباً"), ("±", "جمع تفریق"), ("©", "کاپی رائٹ")];

/// Replaces ASCII spaces with non-breaking space `\u{a0}` (pynini `convert_space`).
pub fn space_to_nbsp(s: &str) -> String {
    s.replace(' ', "\u{a0}")
}

/// Checks if a character is an Arabic/Urdu script character used in words.
#[inline]
pub fn is_urdu_char(c: char) -> bool {
    matches!(c,
        '\u{0621}'..='\u{064A}' |
        '\u{064B}'..='\u{065F}' |
        '\u{0670}' |
        '\u{0671}'..='\u{06D3}'
    )
}

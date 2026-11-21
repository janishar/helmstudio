//! Arabic fraction classifier and verbalizer.
//! Matching `ar/taggers/fraction.py` and `ar/verbalizers/fraction.py`.

use super::cardinal::{parse_cardinal_span, spell_cardinal};
use super::char_util::{char_at, token_end, token_start};
use crate::lang::common::{Fields, Punctuation};
use crate::lattice::{Candidate, Classifier};
use crate::tag::Dict;
use crate::text::cost;

const UNICODE_FRACTIONS: &[(char, u64, u64)] = &[
    ('¼', 1, 4),
    ('½', 1, 2),
    ('¾', 3, 4),
    ('⅐', 1, 7),
    ('⅑', 1, 9),
    ('⅒', 1, 10),
    ('⅓', 1, 3),
    ('⅔', 2, 3),
    ('⅕', 1, 5),
    ('⅖', 2, 5),
    ('⅗', 3, 5),
    ('⅘', 4, 5),
    ('⅙', 1, 6),
    ('⅚', 5, 6),
    ('⅛', 1, 8),
    ('⅜', 3, 8),
    ('⅝', 5, 8),
    ('⅞', 7, 8),
];

const FRAC_SINGULAR: [&str; 11] = ["", "", "نصف", "ثلث", "ربع", "خمس", "سدس", "سبع", "ثمن", "تسع", "عشر"];

const FRAC_DUAL: [&str; 11] = ["", "", "نصفين", "ثلثين", "ربعين", "خمسين", "سدسين", "سبعين", "ثمنين", "تسعين", "عشرين"];

const FRAC_PLURAL: [&str; 11] = ["", "", "", "أثلاث", "أرباع", "أخماس", "أسداس", "أسباع", "أثمان", "أتساع", "أعشار"];

pub struct FractionClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for FractionClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }
        let mut base = start;
        let neg = matches!(text.get(base), Some('-' | '−'));
        if neg {
            base += 1;
            if text.get(base) == Some(&' ') {
                base += 1;
            }
        }
        let mut emit_fraction = |fraction_start: usize, int_cost: f64, has_integer: bool, unicode_only: bool| {
            let rank = (if neg { 1 } else { 0 }) | (if has_integer { 4 } else { 0 });
            for &(ch, num, den) in UNICODE_FRACTIONS {
                if text.get(fraction_start) == Some(&ch) && token_end(text, fraction_start + 1, self.punct) {
                    out.push(Candidate {
                        end: fraction_start + 1,
                        cost: cost(1.0 + int_cost),
                        rank,
                        tag: String::new(),
                    });
                    out.push(Candidate {
                        end: fraction_start + 1,
                        cost: cost(1.001 + int_cost),
                        rank: rank | 2,
                        tag: String::new(),
                    });
                    let _ = (num, den);
                }
            }
            if unicode_only {
                return;
            }
            let mut slash = fraction_start;
            while slash < text.len() && (char_at(text, slash).is_ascii_digit() || char_at(text, slash) == ',') {
                slash += 1;
            }
            let Some((num, num_cost)) = parse_cardinal_span(text, fraction_start, slash, false) else { return };
            let den_start = if text.get(slash) == Some(&'/') {
                slash + 1
            } else if text.get(slash) == Some(&' ')
                && text.get(slash + 1) == Some(&'/')
                && text.get(slash + 2) == Some(&' ')
            {
                slash + 3
            } else {
                return;
            };
            let mut end = den_start;
            while end < text.len() && (char_at(text, end).is_ascii_digit() || char_at(text, end) == ',') {
                end += 1;
                if !token_end(text, end, self.punct) {
                    continue;
                }
                let Some((den, den_cost)) = parse_cardinal_span(text, den_start, end, false) else { continue };
                let exact_num = slash - fraction_start == if num == 10 { 2 } else { 1 };
                let exact_den = end - den_start == if den == 10 { 2 } else { 1 };
                let special = exact_num
                    && exact_den
                    && ((num == 1 || num == 2) && (2..=10).contains(&den)
                        || (3..=10).contains(&num) && (3..=10).contains(&den));
                if special {
                    out.push(Candidate { end, cost: cost(1.0 + int_cost + num_cost), rank, tag: String::new() });
                }
                out.push(Candidate {
                    end,
                    cost: cost(1.001 + int_cost + num_cost + den_cost),
                    rank: rank | 2,
                    tag: String::new(),
                });
            }
        };
        emit_fraction(base, 0.0, false, false);
        let mut int_end = base;
        while int_end < text.len() && (char_at(text, int_end).is_ascii_digit() || char_at(text, int_end) == ',') {
            int_end += 1;
            if let Some((_, int_cost)) = parse_cardinal_span(text, base, int_end, false) {
                emit_fraction(int_end, int_cost, true, true);
                if text.get(int_end) == Some(&' ') {
                    emit_fraction(int_end + 1, int_cost, true, false);
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut idx = start;
        let mut neg = false;
        if idx < text.len() && (text[idx] == '-' || text[idx] == '−') {
            neg = true;
            idx += 1;
            if idx < text.len() && text[idx] == ' ' {
                idx += 1;
            }
        }

        let mut int_val: Option<u64> = None;
        if candidate.rank & 4 != 0 {
            let mut int_end = idx;
            while int_end < candidate.end && (char_at(text, int_end).is_ascii_digit() || char_at(text, int_end) == ',')
            {
                int_end += 1;
            }
            int_val = parse_cardinal_span(text, idx, int_end, false).map(|(value, _)| value);
            idx = int_end;
            if text.get(idx) == Some(&' ') {
                idx += 1;
            }
        }

        let mut num: u64 = 0;
        let mut den: u64 = 0;

        for &(ch, u_num, u_den) in UNICODE_FRACTIONS {
            if idx < candidate.end && text[idx] == ch {
                num = u_num;
                den = u_den;
                break;
            }
        }

        if den == 0 {
            let num_start = idx;
            while idx < candidate.end && (char_at(text, idx).is_ascii_digit() || char_at(text, idx) == ',') {
                idx += 1;
            }
            num = parse_cardinal_span(text, num_start, idx, false).map_or(0, |(value, _)| value);
            while idx < candidate.end && (text[idx] == ' ' || text[idx] == '/') {
                idx += 1;
            }
            let den_start = idx;
            den = parse_cardinal_span(text, den_start, candidate.end, false).map_or(0, |(value, _)| value);
        }

        let num_spoken = spell_cardinal(num);
        let den_spoken = if candidate.rank & 2 == 0 && num == 1 && den >= 2 && den <= 10 {
            FRAC_SINGULAR[den as usize].to_string()
        } else if candidate.rank & 2 == 0 && num == 2 && den >= 2 && den <= 10 {
            FRAC_DUAL[den as usize].to_string()
        } else if candidate.rank & 2 == 0 && num >= 3 && num <= 10 && den >= 3 && den <= 10 {
            FRAC_PLURAL[den as usize].to_string()
        } else {
            spell_cardinal(den)
        };

        let mut tag = String::with_capacity(64);
        tag.push_str("fraction { ");
        if neg {
            tag.push_str("negative: \"true\" ");
        }
        if let Some(i) = int_val {
            tag.push_str("integer_part: \"");
            tag.push_str(&spell_cardinal(i));
            tag.push_str("\" ");
        }
        tag.push_str("numerator: \"");
        tag.push_str(&num_spoken);
        tag.push_str("\" denominator: \"");
        tag.push_str(&den_spoken);
        tag.push_str("\" }");
        candidate.tag = tag;
    }
}

pub fn verbalize(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        Some("true") => true,
        None => false,
        _ => return None,
    };
    let int_part = f.take("integer_part");
    let numerator = f.take("numerator")?;
    let denominator = f.take("denominator")?;
    if !f.done() {
        return None;
    }

    let mut out = String::with_capacity(64);
    if neg {
        out.push_str("سالب ");
    }
    if let Some(int_p) = int_part {
        out.push_str(int_p);
        out.push_str(" و ");
    }

    // Special cases according to verbalizers/fraction.py
    if numerator == "واحد" && FRAC_SINGULAR[2..].contains(&denominator) {
        out.push_str(denominator);
    } else if numerator == "اثنان" && FRAC_DUAL[2..].contains(&denominator) {
        out.push_str(denominator);
    } else if matches!(numerator, "ثلاثة" | "أربعة" | "خمسة" | "ستة" | "سبعة" | "ثمانية" | "تسعة" | "عشرة")
        && FRAC_PLURAL[3..].contains(&denominator)
    {
        out.push_str(numerator);
        out.push(' ');
        out.push_str(denominator);
    } else {
        out.push_str(numerator);
        out.push_str(" على ");
        out.push_str(denominator);
    }

    Some(out)
}

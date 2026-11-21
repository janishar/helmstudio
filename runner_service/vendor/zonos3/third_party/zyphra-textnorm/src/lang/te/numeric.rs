//! Classifiers for cardinal, decimal, fraction, and ordinal in Telugu.

use super::lazy;
use super::numbers::{self, is_digit, is_zero};
use crate::lattice::Candidate;
use crate::text::cost;

const QUANTITIES: [&str; 13] =
    ["మిలియన్లు", "బిలియన్లు", "మిలియన్", "బిలియన్", "లక్షలు", "కోట్లు", "లక్షల", "కోట్ల", "వెయ్యి", "వేలు", "వేల", "లక్ష", "కోటి"];

pub fn is_quantity(s: &str) -> bool {
    QUANTITIES.contains(&s)
}

static QUANTITY_ENDS: std::sync::LazyLock<std::collections::HashSet<char>> =
    std::sync::LazyLock::new(|| QUANTITIES.iter().filter_map(|q| q.chars().next_back()).collect());

fn vulgar(c: char) -> bool {
    matches!(
        c,
        '½' | '¼'
            | '¾'
            | '⅓'
            | '⅔'
            | '⅕'
            | '⅖'
            | '⅗'
            | '⅘'
            | '⅙'
            | '⅚'
            | '⅐'
            | '⅛'
            | '⅜'
            | '⅝'
            | '⅞'
            | '⅑'
            | '⅒'
    )
}

pub fn possible_end(c: char) -> bool {
    is_digit(c) || c == 'వ' || vulgar(c) || QUANTITY_ENDS.contains(&c)
}

pub fn structural_char(c: char) -> bool {
    is_digit(c) || matches!(c, ',' | '.' | '/' | ' ') || vulgar(c)
}

pub fn last_lexical_end(input: &str, start: usize) -> Option<usize> {
    let rest = &input[start..];
    QUANTITIES.iter().copied().chain(["వ"]).filter(|key| rest.starts_with(key)).map(|key| start + key.len()).max()
}

pub fn possible_prefix(s: &str) -> bool {
    let s = s.strip_prefix('-').unwrap_or(s);
    let lexical = s.char_indices().find(|&(_, c)| !is_digit(c) && !matches!(c, ',' | '.' | '/' | ' ') && !vulgar(c));
    match lexical {
        None => true,
        Some((at, _)) => {
            let rest = &s[at..];
            "వ".starts_with(rest) || QUANTITIES.iter().any(|q| q.starts_with(rest))
        }
    }
}

const VULGAR_FRACTIONS: [(char, &str, &str); 18] = [
    ('½', "ఒకటి", "రెండు"),
    ('¼', "ఒకటి", "నాలుగు"),
    ('¾', "మూడు", "నాలుగు"),
    ('⅓', "ఒకటి", "మూడు"),
    ('⅔', "రెండు", "మూడు"),
    ('⅕', "ఒకటి", "ఐదు"),
    ('⅖', "రెండు", "ఐదు"),
    ('⅗', "మూడు", "ఐదు"),
    ('⅘', "నాలుగు", "ఐదు"),
    ('⅙', "ఒకటి", "ఆరు"),
    ('⅚', "ఐదు", "ఆరు"),
    ('⅐', "ఒకటి", "ఏడు"),
    ('⅛', "ఒకటి", "ఎనిమిది"),
    ('⅜', "మూడు", "ఎనిమిది"),
    ('⅝', "ఐదు", "ఎనిమిది"),
    ('⅞', "ఏడు", "ఎనిమిది"),
    ('⅑', "ఒకటి", "తొమ్మిది"),
    ('⅒', "ఒకటి", "పది"),
];

fn parse_sign(s: &str) -> (&str, bool) {
    if let Some(rest) = s.strip_prefix('-') { (rest, true) } else { (s, false) }
}

fn valid_grouping(s: &str) -> Option<String> {
    if !s.contains(',') {
        return None;
    }
    let mut parts = s.split(',').peekable();
    let first = parts.next()?;
    let first_len = first.chars().count();
    if first_len == 0 || !first.chars().all(is_digit) || parts.peek().is_none() {
        return None;
    }
    let mut western = (1..=3).contains(&first_len);
    let mut indian = (1..=2).contains(&first_len);
    while let Some(part) = parts.next() {
        let len = part.chars().count();
        let digits = part.chars().all(is_digit);
        western &= len == 3 && digits;
        indian &= len == (if parts.peek().is_some() { 2 } else { 3 }) && digits;
    }
    (western || indian).then(|| s.chars().filter(|&c| c != ',').collect())
}

fn valid_cardinal(s: &str) -> bool {
    let mut chars = s.chars();
    let Some(first) = chars.next() else { return false };
    let count = s.chars().count();
    count <= 18 && is_digit(first) && (count == 1 || !is_zero(first)) && chars.all(is_digit)
}

#[derive(Clone, Copy)]
pub(super) struct CardinalRecipe<'a> {
    raw: &'a str,
    grouped: bool,
    digitwise: bool,
    pub weight: crate::text::Cost,
}

impl CardinalRecipe<'_> {
    pub fn render(self) -> String {
        if self.grouped {
            let digits: String = self.raw.chars().filter(|&c| c != ',').collect();
            numbers::cardinal(&digits).unwrap()
        } else if self.digitwise {
            numbers::digitwise(self.raw).unwrap()
        } else {
            numbers::cardinal(self.raw).unwrap()
        }
    }
}

pub(super) fn cardinal_recipe(s: &str) -> Option<CardinalRecipe<'_>> {
    if s.contains(',') {
        let mut parts = s.split(',').peekable();
        let first = parts.next()?;
        let count = first.chars().count();
        if count == 0 || !first.chars().all(is_digit) || parts.peek().is_none() {
            return None;
        }
        let mut total = count;
        let mut western = (1..=3).contains(&count);
        let mut indian = (1..=2).contains(&count);
        while let Some(part) = parts.next() {
            let n = part.chars().count();
            let digits = part.chars().all(is_digit);
            total += n;
            western &= n == 3 && digits;
            indian &= n == (if parts.peek().is_some() { 2 } else { 3 }) && digits;
        }
        if !(western || indian) || total > 18 || is_zero(first.chars().next()?) {
            return None;
        }
        Some(CardinalRecipe { raw: s, grouped: true, digitwise: false, weight: 0 })
    } else {
        let mut chars = s.chars();
        let first = chars.next()?;
        if !is_digit(first) || !chars.all(is_digit) {
            return None;
        }
        let count = s.chars().count();
        let digitwise = count > 1 && is_zero(first);
        if !digitwise && count > 18 {
            return None;
        }
        Some(CardinalRecipe { raw: s, grouped: false, digitwise, weight: if digitwise { cost(0.5) } else { 0 } })
    }
}

pub fn materialize_cardinal(s: &str) -> Option<String> {
    let (raw, negative) = parse_sign(s);
    let recipe = cardinal_recipe(raw)?;
    let mut tag = String::from("cardinal { ");
    if negative {
        tag.push_str("negative: \"true\" ");
    }
    tag.push_str("integer: \"");
    if recipe.grouped {
        let digits: String = raw.chars().filter(|&c| c != ',').collect();
        numbers::cardinal_into(&mut tag, &digits)?;
    } else if recipe.digitwise {
        for (i, c) in raw.chars().enumerate() {
            if i != 0 {
                tag.push(' ');
            }
            tag.push_str(numbers::digit_word(c)?);
        }
    } else {
        numbers::cardinal_into(&mut tag, raw)?;
    }
    tag.push_str("\" }");
    Some(tag)
}

fn cardinal_tag(sign: &str, digits: &str) -> String {
    let mut tag = format!("cardinal {{ {sign}integer: \"");
    numbers::cardinal_into(&mut tag, digits).unwrap();
    tag.push_str("\" }");
    tag
}

pub fn classify_cardinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (num_str, negative) = parse_sign(s);
    if num_str.is_empty() {
        return;
    }

    let sign_prefix = if negative { "negative: \"true\" " } else { "" };

    // Grouped with commas
    if let Some(stripped) = valid_grouping(num_str) {
        if valid_cardinal(&stripped) {
            out.push(Candidate {
                end,
                cost: cost(1.1),
                rank: 1,
                tag: lazy::tag(|| cardinal_tag(sign_prefix, &stripped)),
            });
            return;
        }
    }

    if !num_str.chars().all(is_digit) {
        return;
    }

    let char_count = num_str.chars().count();
    let first_char = num_str.chars().next().unwrap();

    // Leading zero with length >= 2
    if char_count >= 2 && is_zero(first_char) {
        out.push(Candidate {
            end,
            cost: cost(1.1) + cost(0.5),
            rank: 1,
            tag: lazy::tag(|| {
                format!("cardinal {{ {sign_prefix}integer: \"{}\" }}", numbers::digitwise(num_str).unwrap())
            }),
        });
        return;
    }

    if valid_cardinal(num_str) {
        out.push(Candidate { end, cost: cost(1.1), rank: 1, tag: lazy::tag(|| cardinal_tag(sign_prefix, num_str)) });
    }
}

pub fn classify_decimal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (num_str, negative) = parse_sign(s);
    if num_str.is_empty() {
        return;
    }

    let sign_prefix = if negative { "negative: \"true\" " } else { "" };

    // Check for quantity suffix
    let mut num_part = num_str;
    let mut quantity_opt = None;
    for q in QUANTITIES {
        if let Some(rest) = num_str.strip_suffix(q) {
            let trimmed = rest.strip_suffix(' ').unwrap_or(rest);
            if !trimmed.is_empty() {
                num_part = trimmed;
                quantity_opt = Some(q);
                break;
            }
        }
    }

    if let Some(q) = quantity_opt {
        // Either cardinal + quantity, or decimal + quantity
        if let Some((int_s, frac_s)) = num_part.split_once('.') {
            if !int_s.is_empty() && !frac_s.is_empty() && int_s.chars().all(is_digit) && frac_s.chars().all(is_digit) {
                if valid_cardinal(int_s) {
                    out.push(Candidate {
                        end,
                        cost: cost(1.1),
                        rank: 2,
                        tag: lazy::tag(|| {
                            let int_spoken = numbers::cardinal(int_s).unwrap();
                            let frac_spoken = numbers::digitwise(frac_s).unwrap();
                            format!(
                                "decimal {{ {sign_prefix}integer_part: \"{int_spoken}\" fractional_part: \"{frac_spoken}\" quantity: \"{q}\" }}"
                            )
                        }),
                    });
                }
            }
        } else if num_part.chars().all(is_digit) {
            if valid_cardinal(num_part) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1),
                    rank: 2,
                    tag: lazy::tag(|| {
                        let int_spoken = numbers::cardinal(num_part).unwrap();
                        format!("decimal {{ {sign_prefix}integer_part: \"{int_spoken}\" quantity: \"{q}\" }}")
                    }),
                });
            }
        }
        return;
    }

    // Bare decimal: integer.fractional
    if let Some((int_s, frac_s)) = num_str.split_once('.') {
        if !int_s.is_empty() && !frac_s.is_empty() && int_s.chars().all(is_digit) && frac_s.chars().all(is_digit) {
            if valid_cardinal(int_s) {
                out.push(Candidate {
                    end,
                    cost: cost(1.1),
                    rank: 2,
                    tag: lazy::tag(|| {
                        let int_spoken = numbers::cardinal(int_s).unwrap();
                        let frac_spoken = numbers::digitwise(frac_s).unwrap();
                        format!(
                            "decimal {{ {sign_prefix}integer_part: \"{int_spoken}\" fractional_part: \"{frac_spoken}\" }}"
                        )
                    }),
                });
            }
        }
    }
}

pub(super) fn fraction_cardinal(s: &str) -> Option<(String, crate::text::Cost)> {
    if let Some(grouped) = valid_grouping(s) {
        return Some((numbers::cardinal(&grouped)?, 0));
    }
    if !s.chars().all(is_digit) || s.is_empty() {
        return None;
    }
    if s.chars().count() > 1 && is_zero(s.chars().next()?) {
        Some((numbers::digitwise(s)?, cost(0.5)))
    } else {
        Some((numbers::cardinal(s)?, 0))
    }
}

pub fn classify_fraction(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let (body, negative) = parse_sign(s);
    let sign = if negative { "negative: \"true\" " } else { "" };
    let special = match body {
        "½" => Some("సగం"),
        "¼" => Some("పావు"),
        "¾" => Some("ముప్పావు"),
        _ => body.split_once('/').and_then(|(a, b)| {
            if a.chars().count() != 1 || b.chars().count() != 1 {
                return None;
            }
            match (numbers::digit_val(a.chars().next()?), numbers::digit_val(b.chars().next()?)) {
                (Some(1), Some(2)) => Some("సగం"),
                (Some(1), Some(4)) => Some("పావు"),
                (Some(3), Some(4)) => Some("ముప్పావు"),
                _ => None,
            }
        }),
    };
    if let Some(spoken) = special {
        out.push(Candidate {
            end,
            cost: cost(1.05) - cost(0.3),
            rank: 3,
            tag: lazy::tag(|| format!("fraction {{ {sign}morphosyntactic_features: \"{spoken}\" }}")),
        });
    }
    let half_integer = body
        .strip_suffix('½')
        .map(|s| s.strip_suffix(' ').unwrap_or(s))
        .or_else(|| body.strip_suffix(" 1/2"))
        .or_else(|| body.strip_suffix(" ౧/౨"));
    if let Some((integer, inner_cost)) = half_integer.and_then(fraction_cardinal) {
        if integer.ends_with('ు') || integer.ends_with('ి') {
            out.push(Candidate {
                end,
                cost: cost(1.05) - cost(0.3) + inner_cost,
                rank: 3,
                tag: lazy::tag(|| format!("fraction {{ {sign}morphosyntactic_features: \"{integer}న్నర\" }}")),
            });
        }
    }
    for &(glyph, numerator, denominator) in &VULGAR_FRACTIONS {
        if body.len() == glyph.len_utf8() && body.starts_with(glyph) && !matches!(glyph, '½' | '¼' | '¾') {
            out.push(Candidate {
                end,
                cost: cost(1.05),
                rank: 3,
                tag: lazy::tag(|| {
                    format!("fraction {{ {sign}numerator: \"{numerator}\" denominator: \"{denominator}\" }}")
                }),
            });
        }
        if let Some(prefix) = body.strip_suffix(glyph) {
            let prefix = prefix.strip_suffix(' ').unwrap_or(prefix);
            if let Some((integer, inner_cost)) = fraction_cardinal(prefix) {
                out.push(Candidate { end, cost: cost(1.05) + inner_cost, rank: 3,
                    tag: lazy::tag(|| format!("fraction {{ {sign}integer_part: \"{integer}\" numerator: \"{numerator}\" denominator: \"{denominator}\" }}")) });
            }
        }
    }
    if let Some((left, right)) = body.split_once('/') {
        let (left, right) = match (left.strip_suffix(' '), right.strip_prefix(' ')) {
            (Some(left), Some(right)) => (left, right),
            (None, None) => (left, right),
            _ => return,
        };
        let Some((denominator, den_cost)) = fraction_cardinal(right) else { return };
        let (integer, numerator) = if let Some((integer, numerator)) = left.split_once(' ') {
            (Some(integer), numerator)
        } else {
            (None, left)
        };
        let Some((numerator, num_cost)) = fraction_cardinal(numerator) else { return };
        if let Some(integer) = integer {
            let Some((integer, int_cost)) = fraction_cardinal(integer) else { return };
            out.push(Candidate { end, cost: cost(1.05) + int_cost + num_cost + den_cost, rank: 3,
                tag: lazy::tag(|| format!("fraction {{ {sign}integer_part: \"{integer}\" numerator: \"{numerator}\" denominator: \"{denominator}\" }}")) });
        } else {
            out.push(Candidate {
                end,
                cost: cost(1.05) + num_cost + den_cost,
                rank: 3,
                tag: lazy::tag(|| {
                    format!("fraction {{ {sign}numerator: \"{numerator}\" denominator: \"{denominator}\" }}")
                }),
            });
        }
    }
}

pub fn classify_ordinal(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let Some(num_str) = s.strip_suffix("వ") else { return };
    if num_str.is_empty() || numbers::is_zero(num_str.chars().next().unwrap()) || !num_str.chars().all(is_digit) {
        return;
    }
    let Some(val) = numbers::parse_digits_u64(num_str) else { return };
    if (1..10_000_000).contains(&val) {
        out.push(Candidate {
            end,
            cost: cost(1.1),
            rank: 4,
            tag: lazy::tag(|| format!("ordinal {{ integer: \"{}\" }}", numbers::spell_ordinal(val).unwrap())),
        });
    }
}

pub fn classify(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let Some(last) = s.chars().next_back() else { return };
    if is_digit(last) {
        classify_cardinal(s, end, out);
    }
    if (is_digit(last) && s.contains('.')) || QUANTITY_ENDS.contains(&last) {
        classify_decimal(s, end, out);
    }
    if vulgar(last) || (is_digit(last) && s.contains('/')) {
        classify_fraction(s, end, out);
    }
    if last == 'వ' {
        classify_ordinal(s, end, out);
    }
}

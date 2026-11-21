//! Swedish fraction classifier.

use super::number::{cardinal, ordinal, ordinal_to_fraction_denominator};
use crate::text::is_nemo_space;

crate::tsv!(VULGAR_FRACTIONS, "sv/numbers/fraction.tsv");

pub(super) fn starts_vulgar(text: &[char], start: usize) -> bool {
    VULGAR_FRACTIONS.prefix_matches(text, start).next().is_some()
}

/// Every fraction path contains a slash or a key from the vulgar table; before
/// that marker it contains only cardinal digits and NEMO whitespace.
pub(super) fn marker_possible(text: &[char], start: usize) -> bool {
    static INITIALS: std::sync::LazyLock<(std::collections::HashSet<char>, bool)> = std::sync::LazyLock::new(|| {
        let initials: std::collections::HashSet<char> =
            VULGAR_FRACTIONS.rows().iter().filter_map(|row| row.key.chars().next()).collect();
        let numeric = initials.iter().any(|c| c.is_ascii_digit() || is_nemo_space(*c));
        (initials, numeric)
    });
    for &c in &text[start..] {
        if c == '/' {
            return true;
        }
        if c.is_ascii_digit() || is_nemo_space(c) {
            if INITIALS.1 && INITIALS.0.contains(&c) {
                return true;
            }
        } else {
            return INITIALS.0.contains(&c);
        }
    }
    false
}
/// Enumerate sentence-level fraction paths without rendering losing paths.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let negative = matches!(text.get(start), Some('-' | '−'));
    let pos = start + usize::from(negative);
    let mut rank = 0;
    recipes(text, pos, |recipe| {
        sink.push(recipe.end, crate::text::cost(1.10), 1300 + rank, || {
            let sign = if negative { "negative: \"true\" " } else { "" };
            format!("fraction {{ {sign}{} }}", recipe.render(false))
        });
        rank += 1;
    });
}

/// Borrowed source fraction.graph path. Signs belong to its caller.
#[derive(Clone, Copy)]
pub(super) struct FractionRecipe<'a> {
    pub end: usize,
    pub cost: crate::text::Cost,
    integer: Option<&'a [char]>,
    numerator: FractionNumber<'a>,
    denominator: FractionNumber<'a>,
}

#[derive(Clone, Copy)]
enum FractionNumber<'a> {
    Input(&'a [char]),
    Literal(&'a str),
}

impl FractionNumber<'_> {
    fn digits(self) -> String {
        match self {
            Self::Input(chars) => chars.iter().filter(|c| c.is_ascii_digit()).collect(),
            Self::Literal(s) => s.to_owned(),
        }
    }
}

impl FractionRecipe<'_> {
    /// Render the fields inside fraction { ... }, without the optional sign.
    pub(super) fn render(self, neuter: bool) -> String {
        let mut out = String::new();
        if let Some(integer) = self.integer {
            let digits: String = integer.iter().filter(|c| c.is_ascii_digit()).collect();
            if let Some(value) = cardinal(&digits, false, false) {
                out.push_str(&format!("integer_part: \"{value}\" "));
            }
        }
        let numerator = cardinal(&self.numerator.digits(), true, false).unwrap_or_default();
        let ord = ordinal(&self.denominator.digits()).unwrap_or_default();
        let mut denominator = ordinal_to_fraction_denominator(&ord);
        if neuter && numerator == "en" && denominator == "halv" {
            denominator = "ett halvt".to_string();
        }
        out.push_str(&format!("numerator: \"{numerator}\" denominator: \"{denominator}\""));
        out
    }
}

fn ordinal_recipes<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, &'a [char], crate::text::Cost)) {
    super::cardinal::recipes(text, start, false, false, |number| {
        let mut value = text[start..number.end]
            .iter()
            .filter(|c| c.is_ascii_digit())
            .fold(0u128, |value, c| value * 10 + (*c as u8 - b'0') as u128);
        while value >= 1000 {
            value /= 1000;
            if (2..100).contains(&(value % 1000)) {
                return;
            }
        }
        emit(number.end, &text[start..number.end], number.cost);
    });
}

fn fraction_body<'a>(
    text: &'a [char],
    start: usize,
    integer: Option<&'a [char]>,
    integer_cost: crate::text::Cost,
    slash: bool,
    vulgar: bool,
    cardinals: Option<&[Option<super::cardinal::CardinalRecipe<'a>>]>,
    mut emit: impl FnMut(FractionRecipe<'a>),
) {
    if slash {
        let mut slash_path = |numerator: super::cardinal::CardinalRecipe<'a>| {
            let p = numerator.end;
            let den_start = if text.get(p) == Some(&'/') {
                Some(p + 1)
            } else if text.get(p..p + 3) == Some(&[' ', '/', ' ']) {
                Some(p + 3)
            } else {
                None
            };
            if let Some(den_start) = den_start {
                ordinal_recipes(text, den_start, |end, denominator, cost| {
                    emit(FractionRecipe {
                        end,
                        cost: integer_cost + numerator.cost + cost,
                        integer,
                        numerator: FractionNumber::Input(&text[start..p]),
                        denominator: FractionNumber::Input(denominator),
                    });
                });
            }
        };
        if let Some(cardinals) = cardinals {
            for &number in cardinals {
                slash_path(number.expect("populated cardinal recipe"));
            }
        } else {
            super::cardinal::recipes(text, start, true, false, &mut slash_path);
        }
    }
    if vulgar {
        for (row, end) in VULGAR_FRACTIONS.prefix_matches(text, start) {
            if let Some((numerator, denominator)) = row.value.split_once('/') {
                emit(FractionRecipe {
                    end,
                    cost: integer_cost + row.cost,
                    integer,
                    numerator: FractionNumber::Literal(numerator),
                    denominator: FractionNumber::Literal(denominator),
                });
            }
        }
    }
}

/// Every endpoint of fraction.graph, with deferred spelling.
pub(super) fn recipes<'a>(text: &'a [char], start: usize, emit: impl FnMut(FractionRecipe<'a>)) {
    if !marker_possible(text, start) {
        return;
    }
    let mut cardinals = [None; 24];
    let mut count = 0;
    super::cardinal::recipes(text, start, false, false, |integer| {
        cardinals[count] = Some(integer);
        count += 1;
    });
    recipes_with_cardinals(text, start, &cardinals[..count], emit);
}

pub(super) fn recipes_with_cardinals<'a>(
    text: &'a [char],
    start: usize,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'a>>],
    mut emit: impl FnMut(FractionRecipe<'a>),
) {
    if !marker_possible(text, start) {
        return;
    }
    fraction_body(text, start, None, 0, true, true, Some(cardinals), &mut emit);
    for &integer in cardinals {
        let integer = integer.expect("populated cardinal recipe");
        let p = integer.end;
        // Slash forms require exactly one literal space. Vulgar forms use
        // ensure_space, accepting any amount of NEMO whitespace.
        if text.get(p) == Some(&' ') && marker_possible(text, p + 1) {
            fraction_body(text, p + 1, Some(&text[start..p]), integer.cost, true, false, None, &mut emit);
        }
        let mut q = p;
        while q < text.len() && is_nemo_space(text[q]) {
            q += 1;
        }
        fraction_body(text, q, Some(&text[start..p]), integer.cost, false, true, None, &mut emit);
    }
}

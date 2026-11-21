//! Swedish decimal and quantity classifier.

use super::number::{spell_digit, spell_digits, under_hundred, under_thousand};
use crate::text::Cost;

crate::tsv!(MILLIONS, "sv/numbers/millions.tsv");
crate::tsv!(MILLIONS_ABBR, "sv/numbers/millions_abbr.tsv");

#[derive(Clone, Copy)]
pub(super) struct FractionalRecipe<'a> {
    pub end: usize,
    pub cost: Cost,
    raw: &'a [char],
}

impl FractionalRecipe<'_> {
    pub fn render(self) -> String {
        let raw: String = self.raw.iter().collect();
        read_fractional_part(&raw)
    }
}

/// The unwrapped `decimal.graph`, without the sentence-level dot rules.
pub(super) fn fractional_recipes<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(FractionalRecipe<'a>)) {
    let mut end = start;
    while end < text.len() && end - start < 4 && text[end].is_ascii_digit() {
        end += 1;
        if fractional_valid(&text[start..end]) {
            emit(FractionalRecipe { end, cost: 0, raw: &text[start..end] });
        }
    }
}

#[derive(Clone, Copy)]
pub(super) struct DecimalRecipe<'a> {
    pub end: usize,
    pub cost: Cost,
    pub(super) integer: Option<super::cardinal::CardinalRecipe<'a>>,
    pub(super) fractional: Option<FractionalRecipe<'a>>,
    pub(super) quantity: Option<(&'a str, bool)>,
}

impl DecimalRecipe<'_> {
    /// Render the source field sequence, without the enclosing decimal token.
    pub fn render(self) -> String {
        let mut fields = String::new();
        if let Some(integer) = self.integer {
            fields.push_str("integer_part: \"");
            fields.push_str(&integer.render());
            fields.push('"');
        }
        if let Some(fractional) = self.fractional {
            if !fields.is_empty() {
                fields.push(' ');
            }
            fields.push(' ');
            fields.push_str("fractional_part: \"");
            fields.push_str(&fractional.render());
            fields.push('"');
        }
        if let Some((quantity, plural)) = self.quantity {
            if !fields.is_empty() {
                fields.push(' ');
            }
            fields.push_str("quantity: \"");
            fields.push_str(quantity);
            if plural {
                fields.push_str("er");
            }
            fields.push('"');
        }
        fields
    }
}

fn quantity_recipes<'a>(
    text: &'a [char],
    start: usize,
    singular: bool,
    mut emit: impl FnMut(usize, &'a str, bool, bool),
) {
    if !singular && text[start..].starts_with(&['t', 'u', 's', 'e', 'n']) {
        emit(start + 5, "tusen", false, true);
    }
    for (row, end) in MILLIONS.prefix_matches(text, start) {
        emit(end, &row.value, !singular, false);
        if !singular && text.get(end..end + 2) == Some(&['e', 'r']) {
            emit(end + 2, &row.value, true, false);
        }
    }
    // get_quantity includes abbreviations in both TN variants (`not itn`).
    for (row, end) in MILLIONS_ABBR.prefix_matches(text, start) {
        emit(end, &row.value, !singular, false);
    }
}

pub(super) fn recipes<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    include_abbr: bool,
    emit: impl FnMut(DecimalRecipe<'a>),
) {
    recipes_with_separator(text, start, common, include_abbr, ',', emit);
}

/// `separator` permits a caller to compose the source graph with a period
/// rewrite; restrictions on that composition belong to the caller.
pub(super) fn recipes_with_separator<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    _include_abbr: bool,
    separator: char,
    emit: impl FnMut(DecimalRecipe<'a>),
) {
    // Both graph branches use the same bounded cardinal endpoints; only
    // their grammatical gender differs.
    let mut cardinals = [None; 24];
    let mut count = 0;
    super::cardinal::recipes(text, start, common, false, |integer| {
        cardinals[count] = Some(integer);
        count += 1;
    });
    recipes_with_cardinals(text, start, common, separator, &cardinals[..count], emit);
}

/// Compose decimal paths with already recognized cardinal endpoints.
pub(super) fn recipes_with_cardinals<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    separator: char,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'a>>],
    mut emit: impl FnMut(DecimalRecipe<'a>),
) {
    let mut integer_paths = |integer: Option<super::cardinal::CardinalRecipe<'a>>, at: usize| {
        if text.get(at) != Some(&separator) {
            return;
        }
        fractional_recipes(text, at + 1, |fractional| {
            let cost = integer.map_or(0, |i| i.cost) + fractional.cost;
            emit(DecimalRecipe { end: fractional.end, cost, integer, fractional: Some(fractional), quantity: None });
            let mut qstart = fractional.end;
            if text.get(qstart) == Some(&' ') {
                qstart += 1;
            }
            quantity_recipes(text, qstart, false, |end, quantity, plural, tusen| {
                let integer = integer.map(|mut i| {
                    i.common = !tusen;
                    i
                });
                emit(DecimalRecipe {
                    end,
                    cost,
                    integer,
                    fractional: Some(fractional),
                    quantity: Some((quantity, plural)),
                });
            });
        });
    };
    integer_paths(None, start);
    for &integer in cardinals {
        let mut integer = integer.expect("populated cardinal recipe");
        integer.common = common;
        integer.counted = false;
        integer_paths(Some(integer), integer.end);
    }
    drop(integer_paths);

    for &integer in cardinals {
        let mut integer = integer.expect("populated cardinal recipe");
        integer.common = true;
        integer.counted = false;
        if integer.raw.len() > 3 || integer.raw.contains(&' ') || integer.raw == ['0'] {
            continue;
        }
        let one = integer.raw == ['1'];
        let mut qstart = integer.end;
        if text.get(qstart) == Some(&' ') {
            qstart += 1;
        }
        if one && text[qstart..].starts_with(&['t', 'u', 's', 'e', 'n']) {
            let mut neuter = integer;
            neuter.common = false;
            emit(DecimalRecipe {
                end: qstart + 5,
                cost: 0,
                integer: Some(neuter),
                fractional: None,
                quantity: Some(("tusen", false)),
            });
        }
        quantity_recipes(text, qstart, one, |end, quantity, plural, tusen| {
            let mut integer = integer;
            integer.common = !tusen;
            emit(DecimalRecipe {
                end,
                cost: integer.cost,
                integer: Some(integer),
                fractional: None,
                quantity: Some((quantity, plural)),
            });
        });
    }
}

/// Render a fractional digit sequence as defined by the Swedish cardinal graph.
pub fn read_fractional_part(frac: &str) -> String {
    match frac.len() {
        1 => spell_digit(frac.as_bytes()[0] - b'0').to_string(),
        2 if !frac.starts_with('0') => {
            let mut out = String::new();
            under_hundred(&mut out, frac.parse().unwrap_or(0), false);
            out
        }
        3 if !frac.starts_with('0') => {
            let mut out = String::new();
            under_thousand(&mut out, frac.parse().unwrap_or(0), false);
            out
        }
        _ => spell_digits(frac),
    }
}

fn fractional_valid(s: &[char]) -> bool {
    match s.len() {
        1 | 2 | 4 => true,
        3 => s[0] != '0' || s[2] != '0' || s[1] == '0',
        _ => false,
    }
}

/// Sentence-level decimal paths, retaining the original quantity-first ordering.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let negative = text.get(start) == Some(&'-');
    let pos = start + usize::from(negative);
    let mut rank = 0;
    let mut submit = |recipe: DecimalRecipe<'_>| {
        sink.push(recipe.end, crate::text::cost(1.10) + recipe.cost, 500 + rank, || {
            let sign = if negative { "negative: \"true\" " } else { "" };
            let mut fields = String::new();
            if let Some(integer) = recipe.integer {
                fields.push_str(&format!("integer_part: \"{}\" ", integer.render()));
            }
            if let Some(fractional) = recipe.fractional {
                fields.push_str(&format!("fractional_part: \"{}\"", fractional.render()));
                if recipe.quantity.is_some() {
                    fields.push(' ');
                }
            }
            if let Some((quantity, plural)) = recipe.quantity {
                fields.push_str("quantity: \"");
                fields.push_str(quantity);
                if plural {
                    fields.push_str("er");
                }
                fields.push('"');
            }
            format!("decimal {{ {sign}{fields} }}")
        });
        rank += 1;
    };
    recipes(text, pos, false, true, |recipe| {
        if recipe.fractional.is_none() {
            submit(recipe);
        }
    });
    recipes(text, pos, false, true, |recipe| {
        if recipe.fractional.is_some() {
            submit(recipe);
        }
    });
    recipes_with_separator(text, pos, false, true, '.', |recipe| {
        let Some(integer) = recipe.integer else { return };
        let Some(fractional) = recipe.fractional else { return };
        if integer.raw.contains(&' ') {
            return;
        }
        let frac_start = integer.end + 1;
        let run = text[frac_start..].iter().take_while(|c| c.is_ascii_digit()).count();
        if run == 0 || run == 3 || run > 4 || fractional.end != frac_start + run {
            return;
        }
        if integer.raw.len() <= 2 && run == 2 {
            let hour = integer.raw.iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
            let minute = text[frac_start..frac_start + 2].iter().fold(0u32, |n, c| n * 10 + (*c as u8 - b'0') as u32);
            if hour < 24 && minute < 60 {
                return;
            }
        }
        submit(recipe);
    });
}

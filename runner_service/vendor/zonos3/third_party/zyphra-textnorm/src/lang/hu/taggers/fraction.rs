// Copyright (c) 2026 Zyphra, Inc.
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

use crate::lang::hu::taggers::cardinal;
use crate::lang::hu::taggers::ordinal::cardinal_to_fractional;
use crate::lang::hu::{Lazy, Sink};
use crate::text::{Cost, cost};

pub const WEIGHT: Cost = cost(1.09);

const VULGAR_FRACTIONS: [(char, u64, u64); 19] = [
    ('⅓', 1, 3),
    ('⅔', 2, 3),
    ('⅕', 1, 5),
    ('⅖', 2, 5),
    ('⅗', 3, 5),
    ('⅘', 4, 5),
    ('⅙', 1, 6),
    ('⅚', 5, 6),
    ('⅐', 1, 7),
    ('⅛', 1, 8),
    ('⅜', 3, 8),
    ('⅝', 5, 8),
    ('⅞', 7, 8),
    ('⅑', 1, 9),
    ('⅒', 1, 10),
    ('¼', 1, 4),
    ('½', 1, 2),
    ('¾', 3, 4),
    ('↉', 0, 3),
];
pub(in crate::lang::hu) fn is_vulgar_fraction(c: char) -> bool {
    VULGAR_FRACTIONS.iter().any(|v| v.0 == c)
}

#[derive(Clone, Copy)]
pub struct RawFraction {
    pub end: usize,
    pub cost: Cost,
    negative: bool,
    integer: Option<u128>,
    numerator: u128,
    denominator: u128,
}

impl RawFraction {
    pub fn render(self) -> String {
        let mut tag = String::new();
        if self.negative {
            tag.push_str("negative: \"true\" ");
        }
        if let Some(n) = self.integer {
            let mut word = String::new();
            cardinal::spell_number(n, &mut word);
            tag.push_str(&format!("integer_part: \"{word}\" "));
        }
        let mut numerator = String::new();
        let mut denominator = String::new();
        cardinal::spell_number(self.numerator, &mut numerator);
        cardinal::spell_number(self.denominator, &mut denominator);
        let denominator = cardinal_to_fractional(&denominator);
        tag.push_str(&format!("numerator: \"{numerator}\" denominator: \"{denominator}\""));
        tag
    }
}

pub fn parse_raw_fraction(text: &[char], start: usize, mut emit: impl FnMut(RawFraction)) {
    let negative = matches!(text.get(start), Some('-' | '−'));
    let p = start + usize::from(negative);
    // Every fraction has either a slash or a vulgar-fraction symbol after
    // its optional integer component; grouped cardinal digits may include
    // periods and spaces before that marker.
    let mut marker = false;
    for &c in text.get(p..).unwrap_or(&[]) {
        if c == '/' || VULGAR_FRACTIONS.iter().any(|v| v.0 == c) {
            marker = true;
            break;
        }
        if !c.is_ascii_digit() && c != '.' && c != ' ' {
            break;
        }
    }
    if !marker {
        return;
    }
    let mut parse = |begin: usize, integer: Option<u128>| {
        if let Some(&c) = text.get(begin) {
            for &(symbol, num, den) in &VULGAR_FRACTIONS {
                if c == symbol {
                    emit(RawFraction {
                        end: begin + 1,
                        cost: integer.map_or(0, cardinal::number_cost),
                        negative,
                        integer,
                        numerator: num as u128,
                        denominator: den as u128,
                    });
                }
            }
        }
        for (num_end, num) in cardinal::numbers(text, begin) {
            let den_start = if text.get(num_end) == Some(&'/') {
                num_end + 1
            } else if text.get(num_end..num_end + 3) == Some(&[' ', '/', ' '][..]) {
                num_end + 3
            } else {
                continue;
            };
            for (end, den) in cardinal::numbers(text, den_start) {
                let cost =
                    integer.map_or(0, cardinal::number_cost) + cardinal::number_cost(num) + cardinal::number_cost(den);
                emit(RawFraction { end, cost, negative, integer, numerator: num, denominator: den });
            }
        }
    };
    parse(p, None);
    for (end, value) in cardinal::numbers(text, p) {
        if text.get(end) == Some(&' ') {
            parse(end + 1, Some(value));
        }
        if text.get(end).is_some_and(|c| VULGAR_FRACTIONS.iter().any(|v| v.0 == *c)) {
            parse(end, Some(value));
        }
    }
}

pub struct Fraction;

impl Lazy for Fraction {
    fn run(&self, text: &[char], start: usize, out: &mut Sink) {
        parse_raw_fraction(text, start, |raw| {
            out.push(raw.end, WEIGHT + raw.cost, 0, || format!("fraction {{ {} preserve_order: true }}", raw.render()));
        });
    }
}

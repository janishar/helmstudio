//! Swedish measure classifier: numbers with units, fractions in measures, measure ranges, math operations, and lone units.

crate::tsv!(UNIT, "sv/measure/unit.tsv");
crate::tsv!(UNIT_NEUTER, "sv/measure/unit_neuter.tsv");
crate::tsv!(UNIT_PLURAL, "sv/measure/unit_plural.tsv");
crate::tsv!(MATH_OPS, "sv/math_operations.tsv");
crate::tsv!(GREEK_LOWER, "sv/measure/greek_lower.tsv");
crate::tsv!(GREEK_UPPER, "sv/measure/greek_upper.tsv");

type UnitIndex = std::collections::HashMap<(char, char), Vec<(usize, &'static crate::text::Row)>>;
static COMMON_CASE_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| case_index(&UNIT));
static NEUTER_CASE_INDEX: std::sync::LazyLock<UnitIndex> = std::sync::LazyLock::new(|| case_index(&UNIT_NEUTER));

fn case_index(table: &'static crate::text::Table) -> UnitIndex {
    let mut index = UnitIndex::new();
    for (order, row) in table.rows().iter().enumerate() {
        let mut chars = row.key.chars();
        if let (Some(first), Some(second)) = (chars.next(), chars.next()) {
            index.entry((first, second)).or_default().push((order, row));
        }
    }
    index
}

// Equations use the counted cardinal graph rather than the standalone
// classifier's signed and serial-number branches.
#[derive(Clone, Copy)]
enum MathTerm<'a> {
    Number(super::cardinal::CardinalRecipe<'a>),
    Letter(char),
    Greek(&'a str, bool),
}

fn swedish_alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || matches!(c, 'Å' | 'Ä' | 'Ö' | 'Ü' | 'É' | 'å' | 'ä' | 'ö' | 'ü' | 'é')
}

fn math_numbers<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, MathTerm<'a>, crate::text::Cost)) {
    super::cardinal::recipes(text, start, false, true, |number| {
        emit(number.end, MathTerm::Number(number), number.cost);
    });
}

fn math_terms<'a>(
    text: &'a [char],
    start: usize,
    greek: bool,
    cardinals: Option<&[Option<super::cardinal::CardinalRecipe<'a>>]>,
    mut emit: impl FnMut(usize, MathTerm<'a>, crate::text::Cost),
) {
    if let Some(cardinals) = cardinals {
        for &number in cardinals {
            let number = number.expect("populated cardinal recipe");
            emit(number.end, MathTerm::Number(number), number.cost);
        }
    } else {
        math_numbers(text, start, &mut emit);
    }
    if let Some(&c) = text.get(start) {
        if swedish_alpha(c) {
            emit(start + 1, MathTerm::Letter(c), 0);
        }
        if greek {
            for (table, upper) in [(&*GREEK_LOWER, false), (&*GREEK_UPPER, true)] {
                for (row, end) in table.prefix_matches(text, start) {
                    emit(end, MathTerm::Greek(&row.value, upper), 0);
                }
            }
        }
    }
}

fn math_delimiters(text: &[char], at: usize, mut emit: impl FnMut(usize)) {
    emit(at);
    if text.get(at) == Some(&' ') {
        emit(at + 1);
    }
}

fn render_math_term(term: MathTerm<'_>, out: &mut String) {
    match term {
        MathTerm::Letter(c) => out.push(c),
        MathTerm::Greek(name, upper) => {
            if upper {
                out.push_str("stort ");
            }
            out.push_str(name);
        }
        MathTerm::Number(number) => out.push_str(&number.render()),
    }
}

/// Emit the two productive equation shapes, retaining every accepting endpoint.
fn classify_math(
    text: &[char],
    start: usize,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'_>>],
    sink: &mut super::Sink<'_>,
) {
    math_terms(text, start, true, Some(cardinals), |a_end, a, ac| {
        math_delimiters(text, a_end, |p| {
            // term op number = term
            for (op, op_end) in MATH_OPS.prefix_matches(text, p) {
                let mut after_op = |q, letter: Option<char>| {
                    math_numbers(text, q, |b_end, b, bc| {
                        math_delimiters(text, b_end, |r| {
                            if text.get(r) != Some(&'=') {
                                return;
                            }
                            math_delimiters(text, r + 1, |s| {
                                math_terms(text, s, true, None, |end, c, cc| {
                                    sink.push(end, crate::text::cost(1.10) + ac + bc + cc, 600, || {
                                        let mut spoken = String::new();
                                        render_math_term(a, &mut spoken);
                                        spoken.push(' '); spoken.push_str(&op.value);
                                        if let Some(c) = letter { spoken.push(c); } else { spoken.push(' '); }
                                        render_math_term(b, &mut spoken);
                                        spoken.push_str(" är ");
                                        render_math_term(c, &mut spoken);
                                        format!("measure {{ units: \"math\" cardinal {{ integer: \"{spoken}\" }} preserve_order: true }}")
                                    });
                                });
                            });
                        });
                    });
                };
                math_delimiters(text, op_end, |q| after_op(q, None));
                if let Some(&c) = text.get(op_end) {
                    if swedish_alpha(c) {
                        after_op(op_end + 1, Some(c));
                    }
                }
            }
            // term = term op number
            if text.get(p) != Some(&'=') {
                return;
            }
            math_delimiters(text, p + 1, |q| {
                math_terms(text, q, false, None, |b_end, b, bc| {
                    math_delimiters(text, b_end, |r| {
                        for (op, op_end) in MATH_OPS.prefix_matches(text, r) {
                            math_delimiters(text, op_end, |s| {
                                math_numbers(text, s, |end, c, cc| {
                                    sink.push(end, crate::text::cost(1.10) + ac + bc + cc, 601, || {
                                        let mut spoken = String::new();
                                        render_math_term(a, &mut spoken);
                                        spoken.push_str(" är ");
                                        render_math_term(b, &mut spoken);
                                        spoken.push(' '); spoken.push_str(&op.value); spoken.push(' ');
                                        render_math_term(c, &mut spoken);
                                        format!("measure {{ units: \"math\" cardinal {{ integer: \"{spoken}\" }} preserve_order: true }}")
                                    });
                                });
                            });
                        }
                    });
                });
            });
        });
    });
}

/// The standalone per-unit path only accepts common-gender units.
pub(super) fn classify_per_unit(text: &[char], start: usize, sink: &mut super::Sink<'_>) {
    let unit_start = if text.get(start) == Some(&'/') {
        let p = start + 1;
        if text.get(p).is_some_and(|&c| crate::text::is_nemo_space(c)) { p + 1 } else { p }
    } else if text.get(start..start + 4) == Some(&['p', 'e', 'r', ' ']) {
        start + 4
    } else {
        return;
    };
    unit_rows(text, unit_start, false, |end, row| {
        sink.push(end, crate::text::cost(1.10) + row.cost, 602, || {
            let unit = row.value.replace(' ', "\u{a0}");
            format!("measure {{ cardinal {{ integer: \"-\" }} units: \"per\u{a0}{unit}\" preserve_order: true }}")
        });
    });
}

#[derive(Clone, Copy)]
struct UnitRecipe<'a> {
    end: usize,
    first: Option<&'a str>,
    per: Option<&'a str>,
    cost: crate::text::Cost,
}

impl UnitRecipe<'_> {
    fn render(self) -> String {
        let mut result = String::new();
        self.append(&mut result);
        result
    }

    fn append(self, result: &mut String) {
        if let Some(first) = self.first {
            result.extend(first.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
        }
        if let Some(per) = self.per {
            if self.first.is_some() {
                result.push('\u{a0}');
            }
            result.push_str("per\u{a0}");
            result.extend(per.chars().map(|c| if c == ' ' { '\u{a0}' } else { c }));
        }
    }
}

fn unit_rows<'a>(text: &'a [char], start: usize, neuter: bool, mut emit: impl FnMut(usize, &'a crate::text::Row)) {
    let table = if neuter { &*UNIT_NEUTER } else { &*UNIT };
    for (row, end) in table.prefix_matches(text, start) {
        emit(end, row);
    }
    if !text.get(start).is_some_and(|c| c.is_ascii_uppercase() || matches!(c, 'Å' | 'Ä' | 'Ö' | 'Ü' | 'É')) {
        return;
    }
    let Some(&second) = text.get(start + 1) else { return };
    if !swedish_alpha(second) {
        return;
    }
    let initial = text[start].to_lowercase().next().unwrap_or(text[start]);
    let lower = second.to_lowercase().next().unwrap_or(second);
    let index = if neuter { &*NEUTER_CASE_INDEX } else { &*COMMON_CASE_INDEX };
    let mut folded = index.get(&(initial, lower)).into_iter().flatten().peekable();
    let mut exact = (second != lower).then(|| index.get(&(initial, second))).flatten().into_iter().flatten().peekable();
    loop {
        let next = match (folded.peek(), exact.peek()) {
            (Some(a), Some(b)) if a.0 <= b.0 => folded.next(),
            (Some(_), Some(_)) => exact.next(),
            (Some(_), None) => folded.next(),
            (None, Some(_)) => exact.next(),
            (None, None) => break,
        };
        let &(_, row) = next.expect("nonempty unit index");
        let mut end = start;
        let mut valid = true;
        for c in row.key.chars() {
            let Some(&actual) = text.get(end) else {
                valid = false;
                break;
            };
            if !swedish_alpha(actual) || (actual.to_lowercase().next() != Some(c) && (end == start || actual != c)) {
                valid = false;
                break;
            }
            end += 1;
        }
        if valid && end - start >= 2 {
            emit(end, row);
        }
    }
}

fn per_units<'a>(text: &'a [char], start: usize, mut emit: impl FnMut(usize, &'a str, crate::text::Cost)) {
    if text.get(start) != Some(&'/') {
        return;
    }
    let p = start + 1;
    for q in [p, p + usize::from(text.get(p).is_some_and(|&c| crate::text::is_nemo_space(c)))] {
        for neuter in [false, true] {
            unit_rows(text, q, neuter, |end, row| emit(end, &row.value, row.cost));
        }
        if q == p && !text.get(p).is_some_and(|&c| crate::text::is_nemo_space(c)) {
            break;
        }
    }
}

fn units<'a>(text: &'a [char], start: usize, neuter: bool, plural: bool, mut emit: impl FnMut(UnitRecipe<'a>)) {
    per_units(text, start, |end, per, cost| emit(UnitRecipe { end, first: None, per: Some(per), cost }));
    unit_rows(text, start, neuter, |end, row| {
        let mut with_first = |first: &'a str, cost| {
            emit(UnitRecipe { end, first: Some(first), per: None, cost });
            per_units(text, end, |last, per, extra| {
                emit(UnitRecipe { end: last, first: Some(first), per: Some(per), cost: cost + extra })
            });
            if text.get(end).is_some_and(|&c| crate::text::is_nemo_space(c)) {
                per_units(text, end + 1, |last, per, extra| {
                    emit(UnitRecipe { end: last, first: Some(first), per: Some(per), cost: cost + extra })
                });
            }
        };
        if plural {
            for pl in UNIT_PLURAL.exact(&row.value) {
                with_first(&pl.value, row.cost + pl.cost);
            }
        } else {
            with_first(&row.value, row.cost);
        }
    });
}

fn unit_after_number<'a>(
    text: &'a [char],
    start: usize,
    neuter: bool,
    plural: bool,
    mut emit: impl FnMut(UnitRecipe<'a>),
) {
    let mut p = start;
    loop {
        units(text, p, neuter, plural, &mut emit);
        if !text.get(p).is_some_and(|&c| crate::text::is_nemo_space(c)) {
            break;
        }
        p += 1;
    }
}

fn period_valid(text: &[char], start: usize, end: usize) -> bool {
    let Some(dot) = text[start..end].iter().position(|&c| c == '.') else { return false };
    dot > 0
        && text[start..start + dot].iter().all(char::is_ascii_digit)
        && end - start - dot - 1 != 3
        && text[start + dot + 1..end].iter().all(char::is_ascii_digit)
        && end > start + dot + 1
}

#[derive(Clone, Copy)]
struct RangeNumber<'a> {
    integer: super::cardinal::CardinalRecipe<'a>,
    fractional: Option<super::decimal::FractionalRecipe<'a>>,
    negative: bool,
}

impl RangeNumber<'_> {
    fn render(self) -> String {
        let mut s = String::new();
        if self.negative {
            s.push_str("minus ");
        }
        s.push_str(&self.integer.render());
        if let Some(fractional) = self.fractional {
            s.push_str(" komma ");
            s.push_str(&fractional.render());
        }
        s
    }
}

fn range_numbers<'a>(
    text: &'a [char],
    start: usize,
    common: bool,
    mut emit: impl FnMut(usize, crate::text::Cost, RangeNumber<'a>),
) {
    let negative = text.get(start) == Some(&'-');
    let p = start + usize::from(negative);
    super::cardinal::recipes(text, p, common, true, |integer| {
        emit(integer.end, integer.cost, RangeNumber { integer, fractional: None, negative });
        if matches!(text.get(integer.end), Some(',' | '.')) {
            let separator = text[integer.end];
            super::decimal::fractional_recipes(text, integer.end + 1, |fractional| {
                if separator == '.' && !period_valid(text, p, fractional.end) {
                    return;
                }
                emit(
                    fractional.end,
                    integer.cost + fractional.cost,
                    RangeNumber { integer, fractional: Some(fractional), negative },
                );
            });
        }
    });
}

fn classify_ranges(
    text: &[char],
    start: usize,
    cardinals: &[Option<super::cardinal::CardinalRecipe<'_>>],
    sink: &mut super::Sink<'_>,
) {
    if text.get(start) == Some(&'−') {
        return;
    }
    let negative = text.get(start) == Some(&'-');
    let p = start + usize::from(negative);
    for neuter in [false, true] {
        for &integer in cardinals {
            let mut integer = integer.expect("populated cardinal recipe");
            integer.common = !neuter;
            let mut visit = |left_end, left_cost, left: RangeNumber<'_>| {
                let (join, spaced) = if matches!(text.get(left_end), Some('-' | '–' | '—')) {
                    (left_end, false)
                } else if text.get(left_end) == Some(&' ')
                    && matches!(text.get(left_end + 1), Some('-' | '–' | '—'))
                    && text.get(left_end + 2) == Some(&' ')
                {
                    (left_end + 1, true)
                } else {
                    return;
                };
                let right_start = join + 1 + usize::from(spaced);
                range_numbers(text, right_start, !neuter, |right_end, right_cost, right| {
                    unit_after_number(text, right_end, neuter, true, |unit| {
                        sink.push(unit.end, crate::text::cost(1.10) + left_cost + right_cost + unit.cost, 603, || {
                            format!(
                                "measure {{ cardinal {{ integer: \"{} till {}\" }} units: \"{}\" }}",
                                left.render(),
                                right.render(),
                                unit.render()
                            )
                        });
                    });
                });
            };
            visit(integer.end, integer.cost, RangeNumber { integer, fractional: None, negative });
            if matches!(text.get(integer.end), Some(',' | '.')) {
                let separator = text[integer.end];
                super::decimal::fractional_recipes(text, integer.end + 1, |fractional| {
                    if separator == '.' && !period_valid(text, p, fractional.end) {
                        return;
                    }
                    visit(
                        fractional.end,
                        integer.cost + fractional.cost,
                        RangeNumber { integer, fractional: Some(fractional), negative },
                    );
                });
            }
        }
    }
}

pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink<'_>) {
    let negative = matches!(text.get(start), Some('-' | '−'));
    let p = start + usize::from(negative);
    let mut cardinals = [None; 24];
    let mut cardinal_count = 0;
    super::cardinal::recipes(text, p, false, true, |number| {
        cardinals[cardinal_count] = Some(number);
        cardinal_count += 1;
    });
    let math_cardinals = if negative { &cardinals[..0] } else { &cardinals[..cardinal_count] };
    if sink.wants(600) || sink.wants(601) {
        classify_math(text, start, math_cardinals, sink);
    }
    if sink.wants(602) {
        classify_per_unit(text, start, sink);
    }

    let numeric_start =
        text.get(p).is_some_and(|c| c.is_ascii_digit() || *c == ',') || super::fraction::starts_vulgar(text, p);
    if numeric_start {
        let sign = if negative { "negative: \"true\" " } else { "" };
        let has_fraction = sink.wants(605) && super::fraction::marker_possible(text, p);
        if sink.wants(603) && super::numeric_range_marker(text, p) {
            classify_ranges(text, start, &cardinals[..cardinal_count], sink);
        }
        for neuter in [false, true] {
            if sink.wants(604) {
                for &number in &cardinals[..cardinal_count] {
                    let mut number = number.expect("populated cardinal recipe");
                    number.common = !neuter;
                    let singular = number.end == p + 1 && text.get(p) == Some(&'1');
                    unit_after_number(text, number.end, neuter, !singular, |unit| {
                        sink.push(unit.end, crate::text::cost(1.10) + number.cost + unit.cost, 604, || {
                            let mut tag = String::with_capacity(96);
                            tag.push_str("measure { cardinal { ");
                            tag.push_str(sign);
                            tag.push_str("integer: \"");
                            number.append(&mut tag);
                            tag.push_str("\" } units: \"");
                            unit.append(&mut tag);
                            tag.push_str("\" }");
                            tag
                        });
                    });
                }
            }
            if has_fraction {
                super::fraction::recipes_with_cardinals(text, p, &cardinals[..cardinal_count], |fraction| {
                    unit_after_number(text, fraction.end, neuter, true, |unit| {
                        sink.push(unit.end, crate::text::cost(1.10) + fraction.cost + unit.cost, 605, || {
                            format!(
                                "measure {{ fraction {{ {sign}{} }} units: \"{}\" }}",
                                fraction.render(neuter),
                                unit.render()
                            )
                        });
                    });
                });
            }
            if sink.wants(606) {
                super::decimal::recipes_with_cardinals(text, p, !neuter, ',', &cardinals[..cardinal_count], |number| {
                    unit_after_number(text, number.end, neuter, true, |unit| {
                        sink.push(unit.end, crate::text::cost(1.10) + number.cost + unit.cost, 606, || {
                            format!(
                                "measure {{ decimal {{ {sign}{} }} units: \"{}\" }}",
                                number.render(),
                                unit.render()
                            )
                        });
                    });
                });
                super::decimal::recipes_with_cardinals(text, p, !neuter, '.', &cardinals[..cardinal_count], |number| {
                    if !period_valid(text, p, number.end) {
                        return;
                    }
                    unit_after_number(text, number.end, neuter, true, |unit| {
                        sink.push(unit.end, crate::text::cost(1.10) + number.cost + unit.cost, 606, || {
                            format!(
                                "measure {{ decimal {{ {sign}{} }} units: \"{}\" }}",
                                number.render(),
                                unit.render()
                            )
                        });
                    });
                });
            }
        }

        // These three source branches have no optional sign and use the neuter
        // decimal graph. Unlike ordinary measures, they do not delete spaces.
        if !negative && (sink.wants(607) || sink.wants(608) || sink.wants(609) || sink.wants(610)) {
            super::decimal::recipes_with_cardinals(text, start, false, ',', &cardinals[..cardinal_count], |number| {
                for radio in ["AM", "FM"] {
                    if crate::text::starts_with(text, number.end, radio) {
                        sink.push(number.end + 2, crate::text::cost(1.10) + number.cost, 607, || {
                            format!("measure {{ decimal {{ {} }} units: \"{radio}\" }}", number.render())
                        });
                    }
                }
                if matches!(text.get(number.end), Some('x' | 'X')) {
                    for (rank, spoken) in [(608, "x"), (609, " times")] {
                        sink.push(number.end + 1, crate::text::cost(1.10) + number.cost, rank, || {
                            format!("measure {{ decimal {{ {} }} units: \"{spoken}\" }}", number.render())
                        });
                    }
                }
                if text.get(number.end) == Some(&'-') {
                    let a = number.end + 1;
                    let mut end = a;
                    while text.get(end).is_some_and(|&c| swedish_alpha(c)) {
                        end += 1;
                        sink.push(end, crate::text::cost(1.10) + number.cost, 610, || {
                            let unit: String = text[a..end].iter().collect();
                            format!("measure {{ decimal {{ {} }} units: \"{unit}\" }}", number.render())
                        });
                    }
                }
            });
        }
    }
    if sink.wants(611) {
        let mut end = start;
        while text.get(end).is_some_and(|&c| swedish_alpha(c)) {
            end += 1;
            if text.get(end) != Some(&'-') {
                continue;
            }
            super::decimal::recipes(text, end + 1, false, false, |number| {
                sink.push(number.end, crate::text::cost(1.10) + number.cost, 611, || {
                    let unit: String = text[start..=end].iter().collect();
                    format!("measure {{ units: \"{unit}\" decimal {{ {} }} preserve_order: true }}", number.render())
                });
            });
        }
    }
}

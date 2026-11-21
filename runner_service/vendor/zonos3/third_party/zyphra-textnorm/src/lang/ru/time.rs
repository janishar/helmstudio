// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Time classifier, ported from ru/taggers/time.py.

use super::number::{Case, Gender, OrdinalForm, PluralCategory, cardinal, ordinal, plural_category};
use crate::lattice::Candidate;
use crate::text::{Cost, cost};

fn hour_noun(h: u64, case: Case) -> &'static str {
    match case {
        Case::Nom => match plural_category(h) {
            PluralCategory::One => "час",
            PluralCategory::Few => "часа",
            PluralCategory::Many => "часов",
        },
        Case::Gen => match plural_category(h) {
            PluralCategory::One => "часа",
            _ => "часов",
        },
    }
}

fn minute_noun(m: u64, case: Case) -> &'static str {
    match case {
        Case::Nom => match plural_category(m) {
            PluralCategory::One => "минута",
            PluralCategory::Few => "минуты",
            PluralCategory::Many => "минут",
        },
        Case::Gen => match plural_category(m) {
            PluralCategory::One => "минуты",
            _ => "минут",
        },
    }
}

fn second_noun(s: u64) -> &'static str {
    match plural_category(s) {
        PluralCategory::One => "секунда",
        PluralCategory::Few => "секунды",
        PluralCategory::Many => "секунд",
    }
}

fn render_plain_hours(h: u64, case: Case) -> String {
    let num_sp = cardinal(h, Gender::Masc, case);
    let noun = hour_noun(h, case);
    format!("{num_sp} {noun}")
}

fn render_plain_minutes(m: u64, case: Case) -> String {
    let num_sp = cardinal(m, Gender::Fem, case);
    let noun = minute_noun(m, case);
    format!("{num_sp} {noun}")
}

fn render_plain_seconds(s: u64) -> String {
    let num_sp = cardinal(s, Gender::Fem, Case::Nom);
    let noun = second_noun(s);
    format!("{num_sp} {noun}")
}

#[derive(Clone, Copy)]
enum Reading {
    Approx(u64, u64),
    Range(u64, u64, u64, u64),
    Clock(u64, u64, bool),
    Colloquial(u64, u64),
    Plain(u64, u64),
    Seconds(u64, u64, u64),
}

fn recognize_time(s: &str, mut emit: impl FnMut(Reading, Cost, i32)) {
    if let Some(rest) = s.strip_prefix('~') {
        if let Some((h, m)) = rest.trim_start_matches(' ').split_once(':') {
            if let (Ok(h), Ok(m)) = (h.parse::<u64>(), m.parse::<u64>()) {
                if h <= 23 && m <= 59 {
                    emit(Reading::Approx(h, m), cost(1.1), 4);
                }
            }
        }
    }
    let range_s = s.strip_prefix("с ").unwrap_or(s);
    for sep in ['-', '–'] {
        if let Some((t1, t2)) = range_s.split_once(sep) {
            if let (Some((h1, m1)), Some((h2, m2))) = (t1.trim().split_once(':'), t2.trim().split_once(':')) {
                if let (Ok(h1), Ok(m1), Ok(h2), Ok(m2)) =
                    (h1.parse::<u64>(), m1.parse::<u64>(), h2.parse::<u64>(), m2.parse::<u64>())
                {
                    if h1 <= 23 && m1 <= 59 && h2 <= 23 && m2 <= 59 {
                        emit(Reading::Range(h1, m1, h2, m2), cost(1.1), 3);
                    }
                }
            }
        }
    }
    for (marker, pm) in [("am", false), ("a.m.", false), ("pm", true), ("p.m.", true)] {
        let Some(prefix) = s
            .len()
            .checked_sub(marker.len())
            .and_then(|i| s.get(..i).zip(s.get(i..)))
            .and_then(|(prefix, suffix)| suffix.eq_ignore_ascii_case(marker).then_some(prefix))
        else {
            continue;
        };
        let time_part = prefix.trim_end();
        let (h, m) = if let Some((h, m)) = time_part.split_once(':') {
            (h.parse::<u64>(), m.parse::<u64>())
        } else {
            (time_part.parse::<u64>(), Ok(0))
        };
        if let (Ok(h), Ok(m)) = (h, m) {
            if h <= 12 && m <= 59 {
                emit(Reading::Clock(h, m, pm), cost(1.1), 2);
            }
        }
    }
    if let Some((h, m)) = s.split_once(':') {
        if let (Ok(h), Ok(m)) = (h.parse::<u64>(), m.parse::<u64>()) {
            if (1..=11).contains(&h) && (1..=59).contains(&m) {
                emit(Reading::Colloquial(h, m), cost(1.1 - 0.1), 1);
            }
        }
    }
    let mut parts = s.split(':');
    if let (Some(h), Some(m)) = (parts.next(), parts.next()) {
        let second = parts.next();
        if parts.next().is_none() {
            if let (Ok(h), Ok(minute)) = (h.parse::<u64>(), m.parse::<u64>()) {
                if h <= 23 && minute <= 59 && m.len() == 2 {
                    match second {
                        None => emit(Reading::Plain(h, minute), cost(1.1), 0),
                        Some(sec) if sec.len() == 2 => {
                            if let Ok(sec) = sec.parse::<u64>() {
                                if sec <= 59 {
                                    emit(Reading::Seconds(h, minute, sec), cost(1.1), 0);
                                }
                            }
                        }
                        _ => {}
                    }
                }
            }
        }
    }
}

fn render_reading(reading: Reading) -> String {
    match reading {
        Reading::Approx(h, m) => {
            let h = render_plain_hours(h, Case::Gen);
            if m == 0 {
                format!("около {h}")
            } else {
                format!("около {h} {}", render_plain_minutes(m, Case::Gen))
            }
        }
        Reading::Range(h1, m1, h2, m2) => {
            let part = |h, m| {
                let hours = render_plain_hours(h, Case::Gen);
                if m == 0 { hours } else { format!("{hours} {}", render_plain_minutes(m, Case::Gen)) }
            };
            format!("с {} до {}", part(h1, m1), part(h2, m2))
        }
        Reading::Clock(h, m, pm) => {
            let word = if matches!(h, 12 | 1 | 2 | 3 | 4) {
                if pm { "дня" } else { "ночи" }
            } else if pm {
                "вечера"
            } else {
                "утра"
            };
            let hours = render_plain_hours(h, Case::Nom);
            if m == 0 {
                format!("{hours} {word}")
            } else {
                format!("{hours} {} {word}", render_plain_minutes(m, Case::Nom))
            }
        }
        Reading::Colloquial(h, m) => {
            let next = h + 1;
            if m <= 29 {
                format!("{} {}", render_plain_minutes(m, Case::Nom), ordinal(next, OrdinalForm::MascGen))
            } else if m == 30 {
                format!("половина {}", ordinal(next, OrdinalForm::MascGen))
            } else {
                let left = 60 - m;
                let noun = if plural_category(left) == PluralCategory::One { "минуты" } else { "минут" };
                format!(
                    "без {} {noun} {}",
                    cardinal(left, Gender::Fem, Case::Gen),
                    cardinal(next, Gender::Masc, Case::Nom)
                )
            }
        }
        Reading::Plain(h, m) => {
            let hours = render_plain_hours(h, Case::Nom);
            if m == 0 { hours } else { format!("{hours} {}", render_plain_minutes(m, Case::Nom)) }
        }
        Reading::Seconds(h, m, sec) => format!(
            "{} {} {}",
            render_plain_hours(h, Case::Nom),
            render_plain_minutes(m, Case::Nom),
            render_plain_seconds(sec)
        ),
    }
}

pub(super) fn span(s: &str, end: usize, out: &mut Vec<Candidate>) {
    let mut idx = 0;
    recognize_time(s, |_, c, r| {
        out.push(Candidate { end, cost: c, rank: r * 10 + idx, tag: String::new() });
        idx += 1;
    });
}
pub(super) fn materialize(s: &str, candidate: &mut Candidate) {
    let mut idx = 0;
    recognize_time(s, |reading, c, r| {
        if r * 10 + idx == candidate.rank && c == candidate.cost {
            candidate.tag = format!("time {{ hours: \"{}\" }}", render_reading(reading));
        }
        idx += 1;
    });
}

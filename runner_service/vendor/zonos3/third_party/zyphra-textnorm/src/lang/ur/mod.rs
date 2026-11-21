//! Urdu text normalization grammar.

pub mod graph_utils;
pub mod lazy;
pub mod taggers;
pub mod verbalize;

use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{cost, is_nemo_space};
use std::sync::LazyLock;
use taggers::Sink;

pub const PREFIX_MODS: &[(&str, &str)] =
    &[("§§", "دفعات"), ("§", "دفعہ"), ("~", "تقریباً"), ("≈", "تقریباً"), ("±", "جمع تفریق"), ("©", "کاپی رائٹ")];

thread_local! {
    static SPAN_INPUT: std::cell::Cell<String> = const { std::cell::Cell::new(String::new()) };
    static SPAN_BATCH: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
    static SPAN_CARDINAL: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
    static SPAN_MONEY: std::cell::Cell<Vec<Candidate>> = const { std::cell::Cell::new(Vec::new()) };
    static ADDRESS_END: std::cell::Cell<Option<usize>> = const { std::cell::Cell::new(None) };
    static PHONE_CONTEXT_END: std::cell::Cell<Option<usize>> = const { std::cell::Cell::new(None) };
}

pub struct Union {
    pub marks: Punctuation,
}

impl Union {
    fn generate(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        let suffix = &text[start..];
        // Modifiers are short; compare directly without rendering the suffix.

        // Check if there is a prefix modifier at start
        let mut prefix_info: Option<(&'static str, usize)> = None;
        for &(pm, spoken) in PREFIX_MODS {
            let p_len = pm.chars().count();
            if suffix.len() >= p_len && suffix[..p_len].iter().copied().eq(pm.chars()) {
                let mut p_len = p_len;
                if suffix.get(p_len) == Some(&' ') {
                    p_len += 1;
                }
                prefix_info = Some((spoken, p_len));
                break;
            }
        }

        // 1. Explore tokens starting at start (no prefix)
        self.generate_span(text, start, start, None, out);

        // 2. Explore semiotic tokens starting after prefix modifier
        if let Some((spoken, p_len)) = prefix_info {
            if p_len < suffix.len() {
                self.generate_span(text, start, start + p_len, Some(spoken), out);
            }
        }
    }

    fn generate_span(
        &self,
        text: &[char],
        _actual_start: usize,
        scan_start: usize,
        prefix_mod: Option<&'static str>,
        out: &mut Vec<Candidate>,
    ) {
        let prefix_cost = if prefix_mod.is_some() { cost(-0.5) } else { 0 };
        let address_future = ADDRESS_END.get().is_some_and(|end| end > scan_start);

        // Some source graphs accept a prefix of a longer numeric-looking run.
        // Evaluate bounded spans so those alternatives are not lost.
        let mut batch = SPAN_BATCH.take();
        let mut cardinals = SPAN_CARDINAL.take();
        batch.clear();
        cardinals.clear();
        taggers::cardinal::classify(text, scan_start, &mut Sink { out: &mut cardinals, target: None, found: None });
        let mut money = SPAN_MONEY.take();
        money.clear();
        let money_start = scan_start + usize::from(text[scan_start] == '-');
        if text.get(money_start).is_some_and(|&c| taggers::money::possible_start(c))
            && taggers::money::reachable_currency(text, money_start)
        {
            taggers::money::classify(text, scan_start, &mut Sink { out: &mut money, target: None, found: None });
        }
        let mut cardinal_index = 0;
        let mut has_slash = false;
        let mut has_period = false;
        let mut date_delimiter = false;
        let mut time_delimiter = false;
        let mut has_address_context = false;
        let mut has_digit = false;
        let mut first_space_seen = false;
        let number_start = scan_start + usize::from(text[scan_start] == '-');
        let first = text.get(number_start).copied();
        let mut numeric_measure_live = true;
        let numeric_measure_start = text[scan_start].is_ascii_digit() || matches!(text[scan_start], '-' | '%' | '٪');
        let mut decimal_live = first.is_some_and(|c| c.is_ascii_digit());
        let mut fraction_live = first.is_some_and(taggers::fraction::possible_start);
        let mut date_live = taggers::date::possible_start(&text[scan_start..]);
        let mut time_live = text[scan_start].is_ascii_digit();
        for end in scan_start + 1..=text.len() {
            let last = text[end - 1];
            has_slash |= last == '/';
            has_period |= last == '.';
            date_delimiter |= matches!(last, '.' | '/' | '-' | '–' | ' ' | ',' | '،');
            time_delimiter |= matches!(last, ':' | '-' | '–' | '+');
            has_digit |= last.is_ascii_digit();
            decimal_live = decimal_live && !is_nemo_space(last);
            fraction_live = fraction_live && taggers::fraction::continuation(last);
            date_live = date_live && taggers::date::continuation(last);
            time_live = time_live && taggers::time::continuation(last);
            if !first_space_seen && is_nemo_space(last) {
                first_space_seen = true;
                let rest = &text[end..];
                let starts_digit = text[scan_start].is_ascii_digit();
                if starts_digit && last == ' ' {
                    date_live = date_live && taggers::date::numeric_space_can_continue(rest);
                    time_live = time_live && taggers::time::first_space_can_continue(rest);
                }
                let percent_before_number = matches!(text[scan_start], '%' | '٪') && !has_digit;
                numeric_measure_live = taggers::measure::numeric_space_can_continue(rest, percent_before_number);
            }
            if address_future && !has_address_context {
                has_address_context = taggers::measure::context_ends_at(&text[scan_start..end]);
            }
            while cardinal_index < cardinals.len() && cardinals[cardinal_index].end < end {
                cardinal_index += 1;
            }
            if !decimal_live
                && !fraction_live
                && !date_live
                && !time_live
                && !(numeric_measure_start && numeric_measure_live)
                && !address_future
                && cardinal_index == cardinals.len()
                && !money.iter().any(|c| c.end >= end)
            {
                break;
            }
            if end < text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]) {
                continue;
            }
            while cardinal_index < cardinals.len() && cardinals[cardinal_index].end == end {
                let c = &cardinals[cardinal_index];
                out.push(Candidate { end, cost: c.cost + prefix_cost, rank: 1, tag: String::new() });
                cardinal_index += 1;
            }
            for rank in [2, 3, 4, 5, 6, 7, 9] {
                let digit_start = first.is_some_and(|c| c.is_ascii_digit()) && number_start < end;
                let possible = match rank {
                    2 => decimal_live && digit_start && (!last.is_ascii_digit() || has_period),
                    3 => {
                        fraction_live
                            && number_start < end
                            && first.is_some_and(|c| taggers::fraction::possible_span(c, last, has_slash))
                    }
                    4 => {
                        date_live
                            && taggers::date::possible_end(last)
                            && (!text[scan_start].is_ascii_digit() || !last.is_ascii_digit() || date_delimiter)
                    }
                    5 => {
                        time_live
                            && text[scan_start].is_ascii_digit()
                            && taggers::time::possible_end(last)
                            && (!last.is_ascii_digit() || time_delimiter)
                    }
                    6 => taggers::measure::possible_span(
                        &text[scan_start..end],
                        has_address_context,
                        numeric_measure_live,
                        address_future,
                    ),
                    7 => true,
                    9 => decimal_live && text[scan_start].is_ascii_digit() && last == 'ں',
                    _ => true,
                };
                if !possible {
                    continue;
                }
                if rank == 7 {
                    for c in &money {
                        if c.end == end {
                            out.push(Candidate { end, cost: c.cost + prefix_cost, rank: 7, tag: String::new() });
                        }
                    }
                    continue;
                }
                batch.clear();
                let mut sink = Sink { out: &mut batch, target: None, found: None };
                let bounded = &text[..end];
                match rank {
                    2 => taggers::decimal::classify(bounded, scan_start, &mut sink),
                    3 => taggers::fraction::classify(bounded, scan_start, &mut sink),
                    4 => taggers::date::classify(bounded, scan_start, &mut sink),
                    5 => taggers::time::classify(bounded, scan_start, &mut sink),
                    6 => taggers::measure::classify_with_address(bounded, scan_start, &mut sink, address_future),
                    9 => taggers::ordinal::classify(bounded, scan_start, &mut sink),
                    _ => unreachable!(),
                }
                for mut c in batch.drain(..) {
                    if c.end == end {
                        c.rank = rank;
                        c.cost += prefix_cost;
                        out.push(c);
                    }
                }
            }
        }
        cardinals.clear();
        SPAN_CARDINAL.set(cardinals);
        batch.clear();
        SPAN_BATCH.set(batch);
        money.clear();
        SPAN_MONEY.set(money);

        let mut slice = SPAN_INPUT.take();
        slice.clear();
        let mut word_possible = true;
        let initial = text[scan_start];
        let mut whitelist_live = taggers::whitelist::possible_start(initial);
        let mut phone_live = taggers::telephone::possible_start(&text[scan_start..]);
        let phone_numeric_start = initial.is_ascii_digit() || matches!(initial, '+' | '(');
        let phone_context_future = PHONE_CONTEXT_END.get().is_some_and(|end| end > scan_start);
        let mut phone_before_space = true;
        let mut phone_digits = 0usize;
        let mut electronic_live = true;
        let mut electronic_marker = false;
        let roman_live = taggers::roman::possible_start(&text[scan_start..]);
        let mut serial_digit = false;
        let mut serial_other = false;
        let mut serial_marker = false;
        for end in scan_start + 1..=text.len() {
            let ch = text[end - 1];
            slice.push(ch);
            let space = is_nemo_space(ch);
            word_possible &= !space;
            if space && !initial.is_ascii_uppercase() {
                whitelist_live = false;
            }
            phone_digits += usize::from(ch.is_ascii_digit());
            serial_digit |= ch.is_ascii_digit();
            serial_other |= !ch.is_ascii_digit();
            serial_marker |= matches!(ch, '-' | '/' | '^' | '.' | '#' | '%' | '&' | '@');
            if phone_numeric_start && (phone_before_space || !phone_context_future) {
                if space {
                    phone_before_space = false;
                } else if !ch.is_ascii_digit() && !matches!(ch, '+' | '-' | '.' | '(' | ')') {
                    phone_live = false;
                }
            }
            electronic_live = electronic_live && taggers::electronic::continuation(ch);
            electronic_marker |= taggers::electronic::marker(ch);
            if !word_possible && !whitelist_live && !phone_live && !electronic_live && !roman_live {
                break;
            }
            if end < text.len() && !is_nemo_space(text[end]) && !self.marks.is_mark(text[end]) {
                continue;
            }
            let first = out.len();
            let mut sink = Sink { out: &mut *out, target: None, found: None };
            if whitelist_live {
                taggers::whitelist::classify(&slice, end, &mut sink);
            }
            if phone_live
                && phone_digits >= 4
                && (ch.is_ascii_digit() || taggers::telephone::context_ends_at(&text[scan_start..end]))
            {
                taggers::telephone::classify(&slice, end, &mut sink);
            }
            if electronic_live && electronic_marker {
                taggers::electronic::classify(&slice, end, &mut sink);
            }
            if word_possible && (serial_marker || (serial_digit && serial_other)) {
                taggers::serial::classify(&slice, end, &mut sink);
            }
            if roman_live {
                taggers::roman::classify(&slice, end, &mut sink);
            }
            if prefix_mod.is_none() && word_possible {
                taggers::word::classify(&slice, end, &mut sink);
            }
            for c in &mut out[first..] {
                c.cost += prefix_cost;
            }
        }
        SPAN_INPUT.set(slice);
    }
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if start == 0 {
            let mut address_end = None;
            let mut phone_end = None;
            for end in (1..=text.len()).rev() {
                let span = &text[..end];
                if address_end.is_none() && taggers::measure::address_ends_at(span) {
                    address_end = Some(end);
                }
                if phone_end.is_none() && taggers::telephone::context_ends_at(span) {
                    phone_end = Some(end);
                }
                if address_end.is_some() && phone_end.is_some() {
                    break;
                }
            }
            ADDRESS_END.set(address_end);
            PHONE_CONTEXT_END.set(phone_end);
        }
        if (start > 0 && !is_nemo_space(text[start - 1]) && !self.marks.is_mark(text[start - 1]))
            || !crate::lang::common::possible_token_start(text, start)
        {
            return;
        }
        lazy::with_build(false, || self.generate(text, start, out));
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        // Reconstruct the selected tag even if a caller supplied a nonempty tag.

        let string_rank = matches!(candidate.rank, 0 | 8 | 10 | 11 | 12);
        let full_input: String = if string_rank { text[start..candidate.end].iter().collect() } else { String::new() };
        let bounded = &text[..candidate.end];

        // Check if this candidate had a prefix_mod
        let mut prefix_info: Option<(&'static str, usize)> = None;
        for &(pm, spoken) in PREFIX_MODS {
            let p_len = pm.chars().count();
            if candidate.end - start >= p_len && text[start..start + p_len].iter().copied().eq(pm.chars()) {
                let mut p_len = p_len;
                if text.get(start + p_len) == Some(&' ') {
                    p_len += 1;
                }
                prefix_info = Some((spoken, p_len));
                break;
            }
        }

        if let Some((spoken, p_len)) = prefix_info {
            let scan_start = start + p_len;
            if scan_start < candidate.end {
                let inner_slice: String =
                    if string_rank { text[scan_start..candidate.end].iter().collect() } else { String::new() };
                let inner_cost = candidate.cost - cost(-0.5);
                let mut matches = Vec::new();
                let mut sink = Sink {
                    out: &mut matches,
                    target: Some((
                        candidate.end,
                        inner_cost,
                        if matches!(candidate.rank, 1..=7 | 9) { 0 } else { candidate.rank },
                    )),
                    found: None,
                };

                lazy::with_build(true, || match candidate.rank {
                    0 => taggers::whitelist::classify(&inner_slice, candidate.end, &mut sink),
                    1 => taggers::cardinal::classify(bounded, scan_start, &mut sink),
                    2 => taggers::decimal::classify(bounded, scan_start, &mut sink),
                    3 => taggers::fraction::classify(bounded, scan_start, &mut sink),
                    4 => taggers::date::classify(bounded, scan_start, &mut sink),
                    5 => taggers::time::classify(bounded, scan_start, &mut sink),
                    6 => taggers::measure::classify(bounded, scan_start, &mut sink),
                    7 => taggers::money::classify(bounded, scan_start, &mut sink),
                    8 => taggers::telephone::classify(&inner_slice, candidate.end, &mut sink),
                    9 => taggers::ordinal::classify(bounded, scan_start, &mut sink),
                    10 => taggers::electronic::classify(&inner_slice, candidate.end, &mut sink),
                    11 => taggers::serial::classify(&inner_slice, candidate.end, &mut sink),
                    12 => taggers::roman::classify(&inner_slice, candidate.end, &mut sink),
                    _ => {}
                });

                if let Some(inner_tag) = sink.found {
                    let inner_tag = match candidate.rank {
                        8 => format!("telephone {{ {inner_tag} }}"),
                        10 => format!("electronic {{ {inner_tag} }}"),
                        12 => format!("roman {{ {inner_tag} }}"),
                        _ => inner_tag,
                    };
                    candidate.tag = format!("name: \"{}\" }} tokens {{ {}", spoken.replace(' ', "\u{a0}"), inner_tag);
                    return;
                }
            }
        }

        // Normal candidate (no prefix_mod)
        if candidate.rank == 13 {
            let mut tag = String::from("name: \"");
            for &c in &text[start..candidate.end] {
                if c == ' ' {
                    tag.push('\u{a0}');
                } else {
                    tag.push(c);
                }
            }
            tag.push('"');
            candidate.tag = tag;
            return;
        }

        let mut matches = Vec::new();
        let mut sink = Sink {
            out: &mut matches,
            target: Some((
                candidate.end,
                candidate.cost,
                if matches!(candidate.rank, 1..=7 | 9) { 0 } else { candidate.rank },
            )),
            found: None,
        };

        lazy::with_build(true, || match candidate.rank {
            0 => taggers::whitelist::classify(&full_input, candidate.end, &mut sink),
            1 => taggers::cardinal::classify(bounded, start, &mut sink),
            2 => taggers::decimal::classify(bounded, start, &mut sink),
            3 => taggers::fraction::classify(bounded, start, &mut sink),
            4 => taggers::date::classify(bounded, start, &mut sink),
            5 => taggers::time::classify(bounded, start, &mut sink),
            6 => taggers::measure::classify(bounded, start, &mut sink),
            7 => taggers::money::classify(bounded, start, &mut sink),
            8 => taggers::telephone::classify(&full_input, candidate.end, &mut sink),
            9 => taggers::ordinal::classify(bounded, start, &mut sink),
            10 => taggers::electronic::classify(&full_input, candidate.end, &mut sink),
            11 => taggers::serial::classify(&full_input, candidate.end, &mut sink),
            12 => taggers::roman::classify(&full_input, candidate.end, &mut sink),
            _ => {}
        });

        if let Some(found_tag) = sink.found {
            candidate.tag = match candidate.rank {
                8 => format!("telephone {{ {found_tag} }}"),
                10 => format!("electronic {{ {found_tag} }}"),
                12 => format!("roman {{ {found_tag} }}"),
                _ => found_tag,
            };
        }
    }
}

struct UrduPunctuation(Punctuation);

impl Classifier for UrduPunctuation {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.0.spans_with_singletons(text, start, &['%', '٪', '‰', '~', '&', '§'], out);
    }
}

pub struct Ur {
    pub classifiers: Vec<Box<dyn Classifier>>,
    punct: UrduPunctuation,
}

impl Grammar for Ur {
    fn code(&self) -> &'static str {
        "ur"
    }

    fn shape(&self) -> Shape {
        Shape::En
    }

    fn classifiers(&self) -> &[Box<dyn Classifier>] {
        &self.classifiers
    }

    fn punct(&self) -> Option<&dyn Classifier> {
        Some(&self.punct)
    }

    fn verbalize(&self, token: &Dict) -> Option<String> {
        verbalize::verbalize(token)
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(16 * split.len());
        for (i, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if i != 0 {
                out.push(' ');
            }
            if let [(name, Value::Str(value))] = body.as_slice() {
                if name == "name" {
                    if value.is_empty() {
                        return None;
                    }
                    out.push_str(value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, Value::Str(value))] = fields.as_slice() {
                        if key == "integer" && !value.contains('"') {
                            out.push_str(value);
                            continue;
                        }
                    }
                }
                if verbalize::append_simple(class, fields, &mut out) {
                    continue;
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered: &Dict| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}

static GRAMMAR: LazyLock<Ur> = LazyLock::new(|| {
    let make = || {
        Punctuation::new(
            taggers::punctuation::URDU_PUNCTUATION_MARKS,
            true,
            std::iter::empty::<&'static str>(),
            cost(2.1),
        )
    };
    Ur { classifiers: vec![Box::new(Union { marks: make() })], punct: UrduPunctuation(make()) }
});

pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

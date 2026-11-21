//! Swedish text normalization grammar.
//!
//! Ported from the production Swedish NeMo grammar:
//! `text_normalization/sv` (deterministic, cased): `ClassifyFst` + `VerbalizeFinalFst`.

mod cardinal;
mod date;
mod decimal;
mod electronic;
mod fraction;
mod measure;
mod money;
mod number;
mod ordinal;
mod roman;
mod telephone;
mod time;
mod whitelist;

use std::sync::LazyLock;

use crate::lang::common::{self, Fields, Punctuation};
use crate::lattice::{Candidate, Classifier, Shape};
use crate::pipeline::Grammar;
use crate::tag::{Dict, Value};
use crate::text::{Cost, cost, is_nemo_space};

/// Swedish Grammar definition.
struct Swedish {
    classifiers: Vec<Box<dyn Classifier>>,
    punct: Punctuation,
}

impl Grammar for Swedish {
    fn code(&self) -> &'static str {
        "sv"
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
        if let Some(s) = common::verbalize_name(token) {
            return Some(s);
        }
        let (k, v) = common::class(token)?;
        let Value::Dict(d) = v else { return None };
        let s = match k {
            "cardinal" => verbalize_cardinal(d)?,
            "ordinal" => verbalize_ordinal(d)?,
            "decimal" => verbalize_decimal(d)?,
            "fraction" => verbalize_fraction(d)?,
            "date" => verbalize_date(d)?,
            "time" => verbalize_time(d)?,
            "money" => verbalize_money(d)?,
            "measure" => verbalize_measure(d)?,
            "electronic" => verbalize_electronic(d)?,
            "telephone" => verbalize_telephone(d)?,
            _ => return None,
        };
        Some(s.replace('\u{a0}', " "))
    }

    fn verbalize_split(&self, split: &[Dict]) -> Option<String> {
        let mut out = String::with_capacity(split.len() * 16);
        let append = |out: &mut String, value: &str| {
            for (index, part) in value.split('\u{a0}').enumerate() {
                if index != 0 {
                    out.push(' ');
                }
                out.push_str(part);
            }
        };
        for (index, token) in split.iter().enumerate() {
            let [(key, Value::Dict(body))] = token.as_slice() else { return None };
            if key != "tokens" {
                return None;
            }
            if index != 0 {
                out.push(' ');
            }
            if let [(key, Value::Str(value))] = body.as_slice() {
                if key == "name" && !value.contains(' ') {
                    append(&mut out, value);
                    continue;
                }
            }
            if let [(class, Value::Dict(fields))] = body.as_slice() {
                if class == "cardinal" {
                    if let [(key, value)] = fields.as_slice() {
                        if key == "integer" {
                            if let Some(value) = common::str_value(value) {
                                if !value.is_empty() {
                                    append(&mut out, value);
                                    continue;
                                }
                            }
                        }
                    }
                }
                if class == "time" {
                    if let [(hour_key, hours), (minute_key, minutes)] = fields.as_slice() {
                        if hour_key == "hours" && minute_key == "minutes" {
                            if let (Some(hours), Some(minutes)) = (common::str_value(hours), common::str_value(minutes))
                            {
                                if !hours.is_empty() && !minutes.is_empty() {
                                    append(&mut out, hours);
                                    if minutes != "noll" {
                                        out.push(' ');
                                        append(&mut out, minutes);
                                    }
                                    continue;
                                }
                            }
                        }
                    }
                }
                if class == "money" {
                    if let [(integer_key, integer), (currency_key, currency)] = fields.as_slice() {
                        if integer_key == "integer_part" && currency_key == "currency_maj" {
                            if let (Some(integer), Some(currency)) =
                                (common::str_value(integer), common::str_value(currency))
                            {
                                if !integer.is_empty() && !currency.is_empty() {
                                    append(&mut out, integer);
                                    out.push(' ');
                                    append(&mut out, currency);
                                    continue;
                                }
                            }
                        }
                    }
                }
                if class == "measure" {
                    if let [(number_key, Value::Dict(number)), (unit_key, Value::Str(unit))] = fields.as_slice() {
                        if number_key == "cardinal" && unit_key == "units" {
                            if let [(integer_key, value)] = number.as_slice() {
                                if integer_key == "integer" {
                                    if let Some(integer) = common::str_value(value) {
                                        if !integer.is_empty() {
                                            if unit == "math" || integer != "-" {
                                                append(&mut out, integer);
                                            }
                                            if unit != "math" {
                                                if integer != "-" {
                                                    out.push(' ');
                                                }
                                                append(&mut out, unit);
                                            }
                                            continue;
                                        }
                                    }
                                }
                            }
                        }
                    }
                }
            }
            let word = crate::permute::first_ordering(body, &mut |ordered| self.verbalize(ordered))?;
            out.push_str(&word);
        }
        Some(out)
    }
}

fn verbalize_cardinal(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        None => false,
        Some("true") => true,
        _ => return None,
    };
    let integer = f.take("integer")?;
    if integer.is_empty() {
        return None;
    }
    if !f.done() {
        return None;
    }
    let sign = if neg { "minus " } else { "" };
    Some(format!("{sign}{integer}"))
}

fn verbalize_ordinal(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let integer = f.take("integer")?;
    if integer.is_empty() {
        return None;
    }
    if !f.done() {
        return None;
    }
    Some(integer.to_string())
}

fn verbalize_decimal(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        None => false,
        Some("true") => true,
        _ => return None,
    };
    let integer = f.opt("integer_part")?;
    let frac = f.opt("fractional_part")?;
    let qty = f.opt("quantity")?;
    let _ = f.preserve_order();
    if !f.done()
        || integer.is_some_and(str::is_empty)
        || frac.is_some_and(str::is_empty)
        || qty.is_some_and(str::is_empty)
    {
        return None;
    }
    let sign = if neg { "minus " } else { "" };
    match (integer, frac, qty) {
        (Some(int), Some(frac), Some(qty)) => Some(format!("{sign}{int} komma {frac} {qty}")),
        (Some(int), Some(frac), None) => Some(format!("{sign}{int} komma {frac}")),
        (Some(int), None, Some(qty)) => Some(format!("{sign}{int} {qty}")),
        _ => None,
    }
}

fn verbalize_fraction(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let neg = match f.opt("negative")? {
        None => false,
        Some("true") => true,
        _ => return None,
    };
    let int_part = f.opt("integer_part")?;
    let num = f.take("numerator")?;
    let den = f.take("denominator")?;
    if !f.done() {
        return None;
    }

    let sign = if neg { "minus " } else { "" };

    let frac_body = if num == "en" || num == "ett" {
        den.to_string()
    } else {
        let den_pl = number::pluralize_fraction_denominator(den);
        format!("{num} {den_pl}")
    };

    if let Some(int) = int_part {
        Some(format!("{sign}{int} och {frac_body}"))
    } else {
        Some(format!("{sign}{frac_body}"))
    }
}

fn verbalize_date(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let day = f.opt("day")?;
    let month = f.opt("month")?;
    let year = f.opt("year")?;
    let era = f.opt("era")?;
    let preserve = f.preserve_order();
    if !f.done()
        || day.is_some_and(str::is_empty)
        || month.is_some_and(str::is_empty)
        || year.is_some_and(str::is_empty)
        || era.is_some_and(str::is_empty)
        || (preserve && day.is_none() && (month.is_some() || era.is_none()))
    {
        return None;
    }

    match (day, month, year, era) {
        (Some(d), Some(m), Some(y), Some(e)) => Some(format!("{d} {m} {y} {e}")),
        (Some(d), Some(m), Some(y), None) => Some(format!("{d} {m} {y}")),
        (Some(d), Some(m), None, None) => Some(format!("{d} {m}")),
        (None, Some(m), Some(y), None) => Some(format!("{m} {y}")),
        (None, None, Some(y), Some(e)) => Some(format!("{y} {e}")),
        (None, None, Some(y), None) => Some(y.to_string()),
        _ => None,
    }
}

fn verbalize_time(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let hours = f.take("hours")?;
    let minutes = f.opt("minutes")?;
    let seconds = f.opt("seconds")?;
    let suffix = f.opt("suffix")?;
    let zone = f.opt("zone")?;
    if !f.done()
        || hours.is_empty()
        || minutes.is_some_and(str::is_empty)
        || seconds.is_some_and(str::is_empty)
        || suffix.is_some_and(str::is_empty)
        || zone.is_some_and(str::is_empty)
        || (seconds.is_some() && minutes.is_none())
    {
        return None;
    }

    let mut out = hours.to_string();
    if let Some(m) = minutes {
        if m != "noll" && !m.is_empty() {
            out.push(' ');
            out.push_str(m);
        }
        if let Some(s) = seconds {
            out.push(' ');
            out.push_str(s);
        }
    }
    if let Some(sfx) = suffix {
        out.push(' ');
        out.push_str(sfx);
    }
    if let Some(z) = zone {
        out.push(' ');
        out.push_str(z);
    }
    Some(out)
}

fn field_str<'a>(fields: &'a Dict, key: &str) -> Option<&'a str> {
    for (k, v) in fields {
        if k == key {
            if let Value::Str(s) = v {
                return Some(s.as_ref());
            }
        }
    }
    None
}

fn field_dict<'a>(fields: &'a Dict, key: &str) -> Option<&'a Dict<'a>> {
    for (k, v) in fields {
        if k == key {
            if let Value::Dict(d) = v {
                return Some(d);
            }
        }
    }
    None
}

fn verbalize_money(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let sign = match f.opt("negative")? {
        None => "",
        Some("true") => "minus ",
        _ => return None,
    };
    let mut body;
    let mut minor = false;
    if f.peek_key() == Some("integer_part") {
        let integer = f.take("integer_part")?;
        if integer.is_empty() {
            return None;
        }
        if f.peek_key() == Some("currency_maj") {
            let maj = f.take("currency_maj")?;
            if maj.is_empty() {
                return None;
            }
            body = format!("{integer} {maj}");
            if f.peek_key() == Some("fractional_part") {
                let frac = f.take("fractional_part")?;
                let unit = f.take("currency_min")?;
                if frac.is_empty() || unit.is_empty() {
                    return None;
                }
                body.push_str(&format!(" {frac} {unit}"));
                minor = true;
            }
        } else {
            let frac = f.opt("fractional_part")?;
            let quantity = f.opt("quantity")?;
            if frac.is_none() && quantity.is_none() {
                return None;
            }
            if frac.is_some_and(str::is_empty) || quantity.is_some_and(str::is_empty) {
                return None;
            }
            let maj = f.take("currency_maj")?;
            if maj.is_empty() {
                return None;
            }
            body = integer.to_string();
            if let Some(frac) = frac {
                body.push_str(&format!(" komma {frac}"));
            }
            if let Some(quantity) = quantity {
                body.push_str(&format!(" {quantity}"));
            }
            body.push_str(&format!(" {maj}"));
        }
    } else {
        let frac = f.take("fractional_part")?;
        let unit = f.take("currency_min")?;
        if frac.is_empty() || unit.is_empty() {
            return None;
        }
        body = format!("{frac} {unit}");
        minor = true;
    }
    if minor {
        f.preserve_order();
    }
    if let Some(per) = f.opt("morphosyntactic_features")? {
        if per.is_empty() {
            return None;
        }
        body.push(' ');
        body.push_str(per);
        f.preserve_order();
    }
    if !f.done() {
        return None;
    }
    Some(format!("{sign}{body}"))
}

fn verbalize_measure(fields: &Dict) -> Option<String> {
    let units = field_str(fields, "units");
    let cardinal_dict = field_dict(fields, "cardinal");
    let decimal_dict = field_dict(fields, "decimal");
    let fraction_dict = field_dict(fields, "fraction");

    if let Some(u) = units {
        if u == "math" {
            if let Some(cd) = cardinal_dict {
                return verbalize_cardinal(cd);
            }
        }
        if let Some(cd) = cardinal_dict {
            if field_str(cd, "integer") == Some("-") {
                return Some(u.to_string());
            }
            let c_spoken = verbalize_cardinal(cd)?;
            if fields.first().map_or(false, |(k, _)| k == "units") && fields.iter().any(|(k, _)| k == "preserve_order")
            {
                return Some(format!("{u} {c_spoken}"));
            }
            return Some(format!("{c_spoken} {u}"));
        }
        if let Some(dd) = decimal_dict {
            let d_spoken = verbalize_decimal(dd)?;
            if fields.first().map_or(false, |(k, _)| k == "units") && fields.iter().any(|(k, _)| k == "preserve_order")
            {
                return Some(format!("{u} {d_spoken}"));
            }
            return Some(format!("{d_spoken} {u}"));
        }
        if let Some(fd) = fraction_dict {
            let f_spoken = verbalize_fraction(fd)?;
            return Some(format!("{f_spoken} {u}"));
        }
        return Some(u.to_string());
    }

    None
}

fn verbalize_electronic(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let protocol = f.opt("protocol")?;
    let username = f.opt("username")?;
    let domain = f.take("domain")?;
    let _ = f.preserve_order();
    if !f.done()
        || domain.is_empty()
        || (protocol.is_some() && username.is_some())
        || protocol.is_some_and(|s| s.is_empty() || s.contains(' '))
        || username.is_some_and(|s| s.is_empty() || s.contains(' '))
    {
        return None;
    }

    let v_domain = verbalize_electronic_text(domain);

    if let Some(user) = username {
        let v_user = verbalize_electronic_characters(user, true);
        Some(format!("{v_user} snabel-a {v_domain}"))
    } else if let Some(prot) = protocol {
        let v_prot = verbalize_electronic_protocol(prot);
        Some(format!("{v_prot} {v_domain}"))
    } else {
        Some(v_domain)
    }
}

fn verbalize_electronic_protocol(p: &str) -> String {
    let s = verbalize_electronic_characters(p, false);
    s.replace("snedstreck snedstreck", "dubbla snedstreck")
}

fn verbalize_electronic_characters(s: &str, digits: bool) -> String {
    let mut out = Vec::new();
    for c in s.chars() {
        let key = c.to_string();
        if let Some(row) = electronic::SYMBOLS.exact(&key).next() {
            out.push(row.value.clone());
        } else if digits && c.is_ascii_digit() {
            out.push(number::spell_digit(c as u8 - b'0').to_string());
        } else {
            out.push(key);
        }
    }
    out.join(" ")
}

fn verbalize_electronic_text(s: &str) -> String {
    use electronic::{DOMAINS, SERVERS, SYMBOLS};
    let chars: Vec<char> = s.chars().collect();
    let mut costs = vec![0usize; chars.len() + 1];
    let mut choices: Vec<Option<(usize, &str)>> = vec![None; chars.len()];
    for i in (0..chars.len()).rev() {
        costs[i] = costs[i + 1] + 1;
        for table in [&*DOMAINS, &*SERVERS] {
            for (row, end) in table.prefix_matches(&chars, i) {
                if end > i && costs[end] < costs[i] {
                    costs[i] = costs[end];
                    choices[i] = Some((end, row.value.as_str()));
                }
            }
        }
    }
    let mut out = Vec::new();
    let mut i = 0;
    while i < chars.len() {
        if let Some((end, spoken)) = choices[i] {
            out.push(spoken.to_string());
            i = end;
            continue;
        }
        let c = chars[i];
        if let Some(row) = SYMBOLS.exact(&c.to_string()).next() {
            out.push(row.value.clone());
        } else if c.is_ascii_digit() {
            out.push(number::spell_digit(c as u8 - b'0').to_string());
        } else {
            out.push(c.to_string());
        }
        i += 1;
    }
    out.join(" ")
}

fn verbalize_telephone(fields: &Dict) -> Option<String> {
    let mut f = Fields::new(fields);
    let cc = f.opt("country_code")?;
    let num = f.take("number_part")?;
    let ext = f.opt("extension")?;
    if !f.done() || num.is_empty() || cc.is_some_and(str::is_empty) || ext.is_some_and(str::is_empty) {
        return None;
    }

    let mut out = String::new();
    if let Some(c) = cc {
        out.push_str(c);
        out.push(' ');
    }
    let num = if num.len() > 1 { num.strip_suffix(' ').unwrap_or(num) } else { num };
    out.push_str(num);
    if let Some(e) = ext {
        out.push_str(" anknytning ");
        out.push_str(e);
    }
    Some(out)
}

/// Sink for zero-allocation candidate exploration and targeted winner materialization.
enum Sink<'a> {
    Explore(&'a mut Vec<Candidate>),
    Find { end: usize, cost: Cost, rank: i32, tag: Option<String> },
}

impl Sink<'_> {
    #[inline]
    fn wants(&self, rank: i32) -> bool {
        match self {
            Sink::Explore(_) => true,
            Sink::Find { rank: requested, tag, .. } => *requested == rank && tag.is_none(),
        }
    }

    #[inline]
    fn push(&mut self, end: usize, cost: Cost, rank: i32, tag: impl FnOnce() -> String) {
        match self {
            Sink::Explore(out) => out.push(Candidate { end, cost, rank, tag: String::new() }),
            Sink::Find { end: e, cost: c, rank: r, tag: t } => {
                if t.is_none() && *e == end && *c == cost && *r == rank {
                    *t = Some(tag());
                }
            }
        }
    }
}

/// Both numeric range graphs place their dash after cardinal/decimal input,
/// ASCII grouping spaces, and (for money) the zero-ending rewrite.
fn numeric_range_marker(text: &[char], start: usize) -> bool {
    for &c in &text[start..] {
        match c {
            '-' | '–' | '—' => return true,
            '0'..='9' | ' ' | ',' | '.' => {}
            _ => return false,
        }
    }
    false
}

/// Unified classifier for all Swedish semiotic classes.
struct Union;

impl Union {
    fn collect_spans(&self, text: &[char], start: usize, sink: &mut Sink, allow_prefix: bool) {
        let c = text[start];

        // 0. Whitelist (cost 1.01, rank 0)
        for (spoken, end) in whitelist::prefix_matches(text, start) {
            sink.push(end, cost(1.01), 0, || {
                let mut tag = String::with_capacity(spoken.len() + 10);
                tag.push_str("name: \"");
                tag.extend(spoken.chars().map(|ch| if ch == ' ' { '\u{a0}' } else { ch }));
                tag.push('"');
                tag
            });
        }

        // 1. Quoted number: '"' + (cardinal | decimal) + '"' (cost 1.08, rank 100)
        if c == '"' && start + 2 < text.len() {
            let num_start = start + 1;
            let mut paths = Vec::new();
            cardinal::classify(text, num_start, &mut Sink::Explore(&mut paths));
            for path in paths {
                if text.get(path.end) == Some(&'"') {
                    sink.push(path.end + 1, cost(1.08) + path.cost - cost(1.10), 100, move || {
                        let mut inner = Sink::Find { end: path.end, cost: path.cost, rank: path.rank, tag: None };
                        cardinal::classify(text, num_start, &mut inner);
                        let Sink::Find { tag: Some(c_tag), .. } = inner else { unreachable!() };
                        format!("name: \"\\\"\" }} tokens {{ {c_tag} }} tokens {{ name: \"\\\"\"")
                    });
                }
            }
            let mut paths = Vec::new();
            decimal::classify(text, num_start, &mut Sink::Explore(&mut paths));
            for path in paths {
                if text.get(path.end) == Some(&'"') {
                    sink.push(path.end + 1, cost(1.08) + path.cost - cost(1.10), 101, move || {
                        let mut inner = Sink::Find { end: path.end, cost: path.cost, rank: path.rank, tag: None };
                        decimal::classify(text, num_start, &mut inner);
                        let Sink::Find { tag: Some(d_tag), .. } = inner else { unreachable!() };
                        format!("name: \"\\\"\" }} tokens {{ {d_tag} }} tokens {{ name: \"\\\"\"")
                    });
                }
            }
        }

        // 2. Date (cost 1.09, rank 200)
        let mut i = 0;
        for (end, tag) in date::match_date(text, start) {
            sink.push(end, cost(1.09), 200 + i, move || tag);
            i += 1;
        }

        // 3–4. Time ranges and individual times.
        time::classify(text, start, sink);

        // 5. Decimal (cost 1.10, rank 500)
        decimal::classify(text, start, sink);

        // 6. Measure, including equations and standalone per-unit expressions.
        measure::classify(text, start, sink);

        // 7. Cardinal range (cost 1.10, rank 700)
        cardinal::classify_range(text, start, sink);

        // 8. Cardinal (cost 1.10, rank 750)
        cardinal::classify(text, start, sink);

        // 9. Ordinal (cost 1.10, rank 800)
        ordinal::classify(text, start, sink);
        // 10. Money and the independent sentence-level money-range graph.
        money::classify(text, start, sink);

        // 12. Telephone (cost 1.10, rank 1100)
        telephone::classify(text, start, sink);

        // 13. Electronic (cost 1.10, rank 1200)
        electronic::classify(text, start, sink);

        // 14. Fraction (cost 1.10, rank 1300)
        fraction::classify(text, start, sink);

        // 15. Roman (cost 1.10, rank 1400)
        for (i, (spoken, end)) in roman::match_roman(text, start).into_iter().enumerate() {
            sink.push(end, cost(1.10), 1400 + i as i32, move || format!("name: \"{spoken}\""));
        }

        // 16. Serial (cost 2.00, rank 1500)
        cardinal::classify_serial(text, start, sink);

        // WordFst accepts every nonempty NEMO_NOT_SPACE span, including punctuation.
        let mut e = start;
        while e < text.len() && !is_nemo_space(text[e]) {
            e += 1;
            sink.push(e, cost(100.0), 1600, || {
                let s = &text[start..e];
                let mut tag = String::with_capacity(s.len() + 10);
                tag.push_str("name: \"");
                tag.extend(s.iter());
                tag.push('"');
                tag
            });
        }

        // 18. Prefix tokens: '~' ("ungefär") and '±' ("plus\u{a0}minus")
        if allow_prefix && (c == '~' || c == '±') {
            let (p_len, p_spoken) = if c == '~' { (1, "ungefär") } else { (1, "plus\u{a0}minus") };
            let mut next = start + p_len;
            if next < text.len() && text[next] == ' ' {
                next += 1;
            }
            if next < text.len() {
                let mut inner = Vec::new();
                self.collect_spans(text, next, &mut Sink::Explore(&mut inner), false);
                for inner_cand in inner {
                    let e = inner_cand.end;
                    let c_cost = inner_cand.cost + cost(1.05);
                    let r = 2000 + inner_cand.rank;
                    sink.push(e, c_cost, r, move || {
                        let mut find_sink =
                            Sink::Find { end: inner_cand.end, cost: inner_cand.cost, rank: inner_cand.rank, tag: None };
                        self.collect_spans(text, next, &mut find_sink, false);
                        if let Sink::Find { tag: Some(inner_tag), .. } = find_sink {
                            format!("name: \"{p_spoken}\" }} tokens {{ {inner_tag}")
                        } else {
                            String::new()
                        }
                    });
                }
            }
        }
    }

    /// Replay possible families in union order; dynamic ranks have no upper cap.
    fn find_spans(&self, text: &[char], start: usize, sink: &mut Sink, allow_prefix: bool) {
        let Sink::Find { end, cost: target_cost, rank, .. } = *sink else {
            self.collect_spans(text, start, sink, allow_prefix);
            return;
        };
        macro_rules! attempt {
            ($condition:expr, $body:expr) => {
                if $condition {
                    $body;
                    if matches!(sink, Sink::Find { tag: Some(_), .. }) {
                        return;
                    }
                }
            };
        }
        if rank == 0 || rank == 100 || rank == 101 {
            self.collect_spans(text, start, sink, allow_prefix);
            return;
        }
        attempt!(rank >= 200 && target_cost == cost(1.09), {
            for (i, (e, tag)) in date::match_date(text, start).into_iter().enumerate() {
                sink.push(e, cost(1.09), 200 + i as i32, move || tag);
            }
        });
        attempt!(rank == 300 || rank == 400, time::classify(text, start, sink));
        attempt!(rank >= 500 && target_cost <= cost(1.10), decimal::classify(text, start, sink));
        attempt!((600..=611).contains(&rank), measure::classify(text, start, sink));
        attempt!(rank >= 700 && target_cost <= cost(1.10), cardinal::classify_range(text, start, sink));
        attempt!(rank >= 750 && target_cost <= cost(1.10), cardinal::classify(text, start, sink));
        attempt!((800..=802).contains(&rank) && target_cost == cost(1.10), ordinal::classify(text, start, sink));
        attempt!(rank == 900 || rank == 1000, money::classify(text, start, sink));
        attempt!(rank == 1100 && target_cost == cost(1.10), telephone::classify(text, start, sink));
        attempt!(rank >= 1200 && target_cost == cost(1.10), electronic::classify(text, start, sink));
        attempt!(rank >= 1300 && target_cost == cost(1.10), fraction::classify(text, start, sink));
        attempt!(rank >= 1400 && target_cost == cost(1.10), {
            for (i, (spoken, e)) in roman::match_roman(text, start).into_iter().enumerate() {
                sink.push(e, cost(1.10), 1400 + i as i32, move || format!("name: \"{spoken}\""));
            }
        });
        attempt!(rank == 1500 && target_cost == cost(2.00), cardinal::classify_serial(text, start, sink));
        if rank == 1600
            && target_cost == cost(100.0)
            && end > start
            && end <= text.len()
            && text[start..end].iter().all(|&c| !is_nemo_space(c))
        {
            sink.push(end, target_cost, rank, || {
                let mut tag = String::with_capacity(end - start + 10);
                tag.push_str("name: \"");
                tag.extend(text[start..end].iter());
                tag.push('"');
                tag
            });
            return;
        }
        if allow_prefix && rank >= 2000 && matches!(text[start], '~' | '±') {
            let spoken = if text[start] == '~' { "ungefär" } else { "plus\u{a0}minus" };
            let mut next = start + 1;
            if text.get(next) == Some(&' ') {
                next += 1;
            }
            if next < text.len() {
                let mut inner = Sink::Find { end, cost: target_cost - cost(1.05), rank: rank - 2000, tag: None };
                self.find_spans(text, next, &mut inner, false);
                if let Sink::Find { tag: Some(tag), .. } = inner {
                    sink.push(end, target_cost, rank, || format!("name: \"{spoken}\" }} tokens {{ {tag}"));
                }
            }
        }
    }
}

impl Classifier for Union {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        self.collect_spans(text, start, &mut Sink::Explore(out), true);
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        if !candidate.tag.is_empty() {
            return;
        }
        let mut sink = Sink::Find { end: candidate.end, cost: candidate.cost, rank: candidate.rank, tag: None };
        self.find_spans(text, start, &mut sink, true);
        if let Sink::Find { tag: Some(tag), .. } = sink {
            candidate.tag = tag;
        }
    }
}

static GRAMMAR: LazyLock<Swedish> =
    LazyLock::new(|| Swedish { classifiers: vec![Box::new(Union)], punct: Punctuation::en(cost(2.1)) });

/// Expose public Swedish grammar entry point.
pub fn grammar() -> &'static dyn Grammar {
    &*GRAMMAR
}

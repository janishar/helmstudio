use super::graph_utils::{all_number, cardinal, glue_suffix, number, ordinal};

pub fn split_suffix(s: &str) -> (&str, Option<&str>) {
    // First bytes of the four source hyphens; avoid decoding every Georgian
    // character when a suffix delimiter cannot possibly occur.
    if !s.bytes().any(|b| matches!(b, b'-' | 0xe2 | 0xd6)) {
        return (s, None);
    }
    if let Some((i, c)) = s.char_indices().rev().find(|(_, c)| matches!(c, '-' | '–' | '—' | '֊')) {
        let suffix = &s[i + c.len_utf8()..];
        if (1..=12).contains(&suffix.chars().count()) && suffix.chars().all(|x| ('\u{10d0}'..='\u{10fa}').contains(&x))
        {
            return (&s[..i], Some(suffix));
        }
    }
    (s, None)
}

/// Alphabet gate for cardinal, list, range, case-suffix, decade and colon
/// branches. Georgian suffix letters only occur after a written dash.
pub fn may_cardinal(s: &str, has_dash: bool) -> bool {
    s.chars().all(|c| {
        c.is_ascii_digit()
            || matches!(c, ' ' | '\u{a0}' | ',' | '.' | ':' | '-' | '−' | '–' | '—' | '֊' | '\'' | 's')
            || (has_dash && ('\u{10d0}'..='\u{10fa}').contains(&c))
    })
}
/// Cost and membership of the unsigned cardinal graph without rendering it.
pub fn all_number_cost(s: &str) -> Option<f64> {
    if s.is_empty() {
        return None;
    }
    if s.bytes().all(|b| b.is_ascii_digit()) {
        return if s.len() <= 15 && (s.len() == 1 || !s.starts_with('0')) { Some(0.0) } else { None };
    }
    let mut groups = s.split(|c| matches!(c, ' ' | '\u{a0}' | ',' | '.'));
    let first = groups.next()?;
    if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
        return None;
    }
    let mut digits = first.len();
    let mut grouped = false;
    for group in groups {
        if group.len() != 3 || !group.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        digits += 3;
        grouped = true;
    }
    if grouped && digits <= 15 { Some(0.01) } else { None }
}

pub fn range_cost(s: &str) -> Option<f64> {
    let (i, c) = s.char_indices().find(|(_, c)| matches!(c, '-' | '–' | '—' | '֊'))?;
    Some(all_number_cost(&s[..i])? + all_number_cost(&s[i + c.len_utf8()..])?)
}

pub fn decimal_cost(s: &str) -> Option<f64> {
    let s = s.strip_prefix(['-', '−']).unwrap_or(s);
    let quantity = s.rsplit_once(' ').and_then(|(a, b)| matches!(b, "ათასი" | "ათას" | "6" | "9" | "12").then_some(a));
    let value = quantity.unwrap_or(s);
    if let Some(pos) = value.rfind(['.', ',']) {
        let weight = all_number_cost(&value[..pos])?;
        let fraction = &value[pos + 1..];
        if fraction.is_empty() || fraction.len() > 11 || !fraction.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let zeros = fraction.bytes().take_while(|&b| b == b'0').count();
        if zeros == fraction.len() {
            if fraction != "0" {
                return None;
            }
        } else if zeros > 5 || fraction.len() - zeros > 6 {
            return None;
        }
        Some(weight)
    } else {
        quantity?;
        let mut count = 0;
        let mut first = 0;
        for b in value.bytes() {
            if b == b' ' {
                continue;
            }
            if !b.is_ascii_digit() {
                return None;
            }
            if count == 0 {
                first = b;
            }
            count += 1;
        }
        if (1..=6).contains(&count) && (count == 1 || first != b'0') { Some(0.0) } else { None }
    }
}

pub fn range_weight(s: &str) -> Option<(String, f64)> {
    let (i, c) = s.char_indices().find(|(_, c)| matches!(c, '-' | '–' | '—' | '֊'))?;
    let (a, aw) = all_number(&s[..i])?;
    let (b, bw) = all_number(&s[i + c.len_utf8()..])?;
    Some((format!("{} {}", glue_suffix(&a, "დან"), glue_suffix(&b, "მდე")), aw + bw))
}

pub fn cardinal_tag(s: &str) -> Vec<(String, f64)> {
    let mut out = Vec::new();
    let (negative, rest) =
        if let Some(r) = s.strip_prefix(['-', '−']) { ("negative: \"true\" ", r) } else { ("", s) };
    if let Some((words, weight)) = number(rest) {
        out.push((format!("cardinal {{ {negative}integer: \"{words}\" }}"), weight));
    }
    if negative.is_empty() {
        if let Some((words, w)) = range_weight(s) {
            out.push((format!("cardinal {{ integer: \"{words}\" }}"), w));
        }
        let (base, suffix) = split_suffix(s);
        if let Some(suf) = suffix {
            if !(base == "1" && (suf.starts_with("ელ") || suf.starts_with('ლ'))) && !suf.starts_with('ე') {
                if let Some((w, weight)) = all_number(base) {
                    out.push((format!("cardinal {{ integer: \"{}\" }}", glue_suffix(&w, suf)), 0.05 + weight));
                }
            }
        }
        let decade = s.strip_suffix("'s").or_else(|| s.strip_suffix('s'));
        if let Some(y) = decade.filter(|y| y.len() == 4 && y.ends_with('0')) {
            if let Some(w) = cardinal(y) {
                out.push((format!("cardinal {{ integer: \"{} წლები\" }}", glue_suffix(&w, "იანი")), -0.3));
            }
        }
        for ending in ["იანი წლები", "იანები", "იანებში", "იანების", "იანებით"]
        {
            if let Some(y) = s.strip_suffix(ending).and_then(|v| v.strip_suffix('-')) {
                if y.len() == 4 && y.ends_with('0') {
                    if let Some(w) = cardinal(y) {
                        out.push((format!("cardinal {{ integer: \"{}\" }}", glue_suffix(&w, ending)), -0.3));
                    }
                }
            }
        }
        if s.bytes().filter(|&b| b == b',').count() >= 2 && s.split(',').all(|x| !x.is_empty() && x.len() <= 3) {
            let vals: Option<Vec<_>> = s.split(',').map(number).collect();
            if let Some(vals) = vals {
                let w = vals.iter().map(|x| x.1).sum();
                let words = vals.into_iter().map(|x| x.0).collect::<Vec<_>>().join(", ");
                out.push((format!("cardinal {{ integer: \"{words}\" }}"), w));
            }
        }
        if let Some((a, b)) = s.split_once(':') {
            if let (Some((a, wa)), Some((b, wb))) = (all_number(a), all_number(b)) {
                out.push((format!("cardinal {{ integer: \"{a} ორწერტილი {b}\" }}"), wa + wb));
            }
        }
    }
    out
}

pub fn ordinal_tag(s: &str) -> Option<String> {
    let (base, suffix) = split_suffix(s);
    if let (Some(case), Some(marker)) = (suffix, split_suffix(base).1) {
        let (digits, _) = split_suffix(base);
        let value = ordinal(digits)?;
        if (digits == "1" && matches!(marker, "ელი" | "ლი")) || (digits != "1" && marker == "ე") {
            return Some(format!("ordinal {{ integer: \"{}\" }}", glue_suffix(&value, case)));
        }
    }
    let suffix = suffix?;
    let value = ordinal(base)?;
    let w = if base == "1" {
        let case = suffix.strip_prefix("ელ").or_else(|| suffix.strip_prefix('ლ'))?;
        match case {
            "ი" => value,
            "ს" => "პირველს".into(),
            "მა" => "პირველმა".into(),
            "ის" => "პირველის".into(),
            "ით" => "პირველით".into(),
            "ად" => "პირველად".into(),
            "ში" => "პირველში".into(),
            "ზე" => "პირველზე".into(),
            "ამდე" => "პირველამდე".into(),
            "იდან" => "პირველიდან".into(),
            _ => return None,
        }
    } else if suffix == "ე" {
        value
    } else if matches!(suffix, "ეს" | "ემ" | "ედ") {
        glue_suffix(&value, suffix)
    } else {
        return None;
    };
    Some(format!("ordinal {{ integer: \"{w}\" }}"))
}

pub fn ordinal_tags(s: &str) -> Vec<String> {
    let Some(tag) = ordinal_tag(s) else { return Vec::new() };
    let digits = s.bytes().take_while(|c| c.is_ascii_digit()).count();
    let mut out = vec![tag.clone()];
    // `one_thousand_ord` and `multi_thousands_ord` both accept 1xxx.
    if digits == 4 && s.starts_with('1') {
        out.push(tag.replacen("integer: \"", "integer: \"ერთი ", 1));
    }
    out
}

pub fn decimal_body_weight(s: &str) -> Option<(String, f64)> {
    let (neg, s) = if let Some(s) = s.strip_prefix(['-', '−']) { ("negative: \"true\" ", s) } else { ("", s) };
    let quantity = s.rsplit_once(' ').and_then(|(a, b)| match b {
        "ათასი" | "ათას" => Some((a, "ათასი")),
        "6" => Some((a, "მილიონი")),
        "9" => Some((a, "მილიარდი")),
        "12" => Some((a, "ტრილიონი")),
        _ => None,
    });
    let (s, q) = quantity.map_or((s, None), |(a, b)| (a, Some(b)));
    let mut body = String::from(neg);
    let mut weight = 0.0;
    if let Some(pos) = s.rfind(['.', ',']) {
        let (a, w) = all_number(&s[..pos])?;
        weight += w;
        let b = &s[pos + 1..];
        if b.is_empty() || b.len() > 11 || !b.bytes().all(|x| x.is_ascii_digit()) {
            return None;
        }
        let leading = b.bytes().take_while(|&x| x == b'0').count();
        let frac = if leading == b.len() {
            if b == "0" { "ნული".to_owned() } else { return None }
        } else {
            if leading > 5 || b.len() - leading > 6 {
                return None;
            }
            format!("{}{}", "ნული ".repeat(leading), cardinal(&b[leading..])?)
        };
        body.push_str(&format!("integer_part: \"{a}\" fractional_part: \"{frac}\""));
    } else {
        if q.is_none() {
            return None;
        }
        let digits = s.replace(' ', "");
        if digits.len() > 6 {
            return None;
        }
        body.push_str(&format!("integer_part: \"{}\"", cardinal(&digits)?));
    }
    if let Some(q) = q {
        body.push_str(&format!(" quantity: \"{q}\""));
    }
    Some((body, weight))
}
pub fn decimal_body(s: &str) -> Option<String> {
    decimal_body_weight(s).map(|v| v.0)
}
pub fn fraction_tags(s: &str) -> Vec<(String, f64)> {
    let (neg, s) = if let Some(x) = s.strip_prefix(['-', '−']) { ("negative: \"true\" ", x) } else { ("", s) };
    let (base, suffix) = split_suffix(s);
    let mut readings: Vec<(Option<String>, String, String, f64)> = Vec::new();
    if let Some((a, b)) = base.split_once(['/', '⁄']) {
        if let Some(ord) = ordinal(b) {
            let den = format!("{}ედი", ord.strip_suffix('ე').unwrap_or(&ord));
            let mut dens = vec![den.clone()];
            if b.len() == 4 && b.starts_with('1') {
                dens.push(format!("ერთი {den}"));
            }
            for den in dens {
                if let Some((num, w)) = all_number(a) {
                    readings.push((None, num, den.clone(), w));
                }
                for (i, _) in a.char_indices().skip(1) {
                    let left = a[..i].strip_suffix(' ').unwrap_or(&a[..i]);
                    let right = &a[i..];
                    if let (Some((whole, wa)), Some((num, wb))) = (all_number(left), all_number(right)) {
                        readings.push((Some(whole), num, den.clone(), wa + wb));
                    }
                }
            }
        }
    } else if let Some((pos, ch)) = base.char_indices().last() {
        let pair = match ch {
            '½' => Some((1, 2)),
            '¼' => Some((1, 4)),
            '¾' => Some((3, 4)),
            '⅓' => Some((1, 3)),
            '⅔' => Some((2, 3)),
            '⅕' => Some((1, 5)),
            '⅖' => Some((2, 5)),
            '⅗' => Some((3, 5)),
            '⅘' => Some((4, 5)),
            '⅙' => Some((1, 6)),
            '⅚' => Some((5, 6)),
            '⅛' => Some((1, 8)),
            '⅜' => Some((3, 8)),
            '⅝' => Some((5, 8)),
            '⅞' => Some((7, 8)),
            '⅐' => Some((1, 7)),
            '⅑' => Some((1, 9)),
            '⅒' => Some((1, 10)),
            _ => None,
        };
        if let Some((a, b)) = pair {
            let raw = base[..pos].strip_suffix(' ').unwrap_or(&base[..pos]);
            let whole = if raw.is_empty() { Some((None, 0.0)) } else { all_number(raw).map(|(v, w)| (Some(v), w)) };
            if let (Some((whole, w)), Some(num), Some(ord)) = (whole, cardinal(&a.to_string()), ordinal(&b.to_string()))
            {
                readings.push((whole, num, format!("{}ედი", ord.strip_suffix('ე').unwrap_or(&ord)), w));
            }
        }
    }
    readings
        .into_iter()
        .map(|(whole, num, den, w)| {
            let den = if let Some(suffix) = suffix { glue_suffix(&den, suffix) } else { den };
            let whole = whole.map(|x| format!("integer_part: \"{x}\" ")).unwrap_or_default();
            (format!("fraction {{ {neg}{whole}numerator: \"{num}\" denominator: \"{den}\" }}"), w)
        })
        .collect()
}

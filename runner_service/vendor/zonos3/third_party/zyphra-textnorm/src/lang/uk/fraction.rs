// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Fraction forms from uk/taggers/fraction.py.

use super::cardinal::{Gender, number};

fn singular(s: &str) -> bool {
    let b = s.as_bytes();
    b.last() == Some(&b'1') && (b.len() == 1 || b[b.len() - 2] != b'1')
}

fn simple(s: &str) -> Option<String> {
    let (numerator, denominator) = s.split_once('/')?;
    let forms = match denominator {
        "2" => ("друга", "других"),
        "3" => ("третя", "третіх"),
        "4" => ("четверта", "четвертих"),
        "5" => ("п'ята", "п'ятих"),
        "6" => ("шоста", "шостих"),
        "7" => ("сьома", "сьомих"),
        "8" => ("восьма", "восьмих"),
        "9" => ("дев'ята", "дев'ятих"),
        "10" => ("десята", "десятих"),
        "12" => ("дванадцята", "дванадцятих"),
        "100" => ("сота", "сотих"),
        "250" => ("двісті п'ятдесята", "двісті п'ятдесятих"),
        "1000" => ("тисячна", "тисячних"),
        _ => return None,
    };
    Some(format!(
        "{} {}",
        number(numerator, Gender::Feminine, false)?,
        if singular(numerator) { forms.0 } else { forms.1 }
    ))
}

fn vulgar(s: &str) -> Option<&'static str> {
    Some(match s {
        "½" => "одна друга",
        "⅓" => "одна третя",
        "⅔" => "дві третіх",
        "¼" => "одна четверта",
        "¾" => "три четвертих",
        "⅕" => "одна п'ята",
        "⅖" => "дві п'ятих",
        "⅗" => "три п'ятих",
        "⅘" => "чотири п'ятих",
        "⅙" => "одна шоста",
        "⅚" => "п'ять шостих",
        "⅛" => "одна восьма",
        "⅜" => "три восьмих",
        "⅝" => "п'ять восьмих",
        "⅞" => "сім восьмих",
        "⅒" => "одна десята",
        _ => return None,
    })
}

pub(super) fn fraction(input: &str) -> Option<String> {
    let (negative, s) = if let Some(s) = input.strip_prefix('-').or_else(|| input.strip_prefix('−')) {
        (true, s)
    } else {
        (false, input)
    };
    let body = if let Some(v) = vulgar(s) {
        v.to_owned()
    } else if let Some((whole, rest)) = s.split_once(' ') {
        let fraction = simple(rest).or_else(|| vulgar(rest).map(str::to_owned))?;
        format!(
            "{} {} {}",
            number(whole, Gender::Feminine, false)?,
            if singular(whole) { "ціла" } else { "цілих" },
            fraction
        )
    } else if let Some((i, _)) = s.char_indices().find(|(_, c)| !c.is_ascii_digit() && *c != '/') {
        let (whole, rest) = s.split_at(i);
        format!(
            "{} {} {}",
            number(whole, Gender::Feminine, false)?,
            if singular(whole) { "ціла" } else { "цілих" },
            vulgar(rest)?
        )
    } else {
        simple(s)?
    };
    Some(if negative { format!("мінус {body}") } else { body })
}

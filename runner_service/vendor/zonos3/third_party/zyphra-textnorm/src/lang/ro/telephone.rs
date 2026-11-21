// Copyright (c) 2026 Zyphra, Inc. Licensed under Apache-2.0.
pub fn telephone(s: &str) -> Option<String> {
    const D: [&str; 10] = ["zero", "unu", "doi", "trei", "patru", "cinci", "șase", "șapte", "opt", "nouă"];
    let (international, s) = if let Some(s) = s.strip_prefix('+') { (true, s) } else { (false, s) };
    if !s.chars().all(|c| c.is_ascii_digit() || matches!(c, ' ' | '-' | '.' | '(' | ')')) {
        return None;
    }
    let digits: String = s.chars().filter(|c| c.is_ascii_digit()).collect();
    if !(7..=15).contains(&digits.len()) {
        return None;
    }
    if !international {
        if !s.trim_start_matches(|c| c == ' ' || c == '(').starts_with('0')
            || s.chars().take_while(|c| *c != '0').count() > 2
            || s.chars().last().is_some_and(|c| !c.is_ascii_digit())
        {
            return None;
        }
        let mut run = 0;
        for c in s.chars() {
            if c.is_ascii_digit() {
                run = 0;
            } else {
                run += 1;
                if run > 2 {
                    return None;
                }
            }
        }
    }
    let words = digits.bytes().map(|b| D[(b - b'0') as usize]).collect::<Vec<_>>().join(" ");
    Some(format!("{}{}", if international { "plus " } else { "" }, words))
}

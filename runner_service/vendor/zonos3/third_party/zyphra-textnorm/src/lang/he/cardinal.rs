//! Productive Hebrew cardinal morphology, matching `he/taggers/cardinal.py`.

const FEM: [&str; 10] = ["אפס", "אחת", "שתיים", "שלוש", "ארבע", "חמש", "שש", "שבע", "שמונה", "תשע"];
const MASC: [&str; 10] = ["אפס", "אחד", "שניים", "שלושה", "ארבעה", "חמישה", "שישה", "שבעה", "שמונה", "תשעה"];
const TEEN_F: [&str; 10] = [
    "עשר",
    "אחת עשרה",
    "שתים עשרה",
    "שלוש עשרה",
    "ארבע עשרה",
    "חמש עשרה",
    "שש עשרה",
    "שבע עשרה",
    "שמונה עשרה",
    "תשע עשרה",
];
const TEEN_M: [&str; 10] = [
    "עשרה",
    "אחד עשר",
    "שנים עשר",
    "שלושה עשר",
    "ארבעה עשר",
    "חמישה עשר",
    "שישה עשר",
    "שבעה עשר",
    "שמונה עשר",
    "תשעה עשר",
];
const TENS: [&str; 10] = ["", "", "עשרים", "שלושים", "ארבעים", "חמישים", "שישים", "שבעים", "שמונים", "תשעים"];
const HUNDREDS: [&str; 10] =
    ["", "מאה", "מאתיים", "שלוש מאות", "ארבע מאות", "חמש מאות", "שש מאות", "שבע מאות", "שמונה מאות", "תשע מאות"];
const THOUSANDS: [&str; 11] = [
    "",
    "אלף",
    "אלפיים",
    "שלושת אלפים",
    "ארבעת אלפים",
    "חמשת אלפים",
    "ששת אלפים",
    "שבעת אלפים",
    "שמונת אלפים",
    "תשעת אלפים",
    "עשרת אלפים",
];

fn under_thousand(n: u32, masculine: bool) -> String {
    let digits = if masculine { &MASC } else { &FEM };
    let teens = if masculine { &TEEN_M } else { &TEEN_F };
    fn append_under_100(out: &mut String, n: u32, digits: &[&str; 10], teens: &[&str; 10]) {
        if n < 10 {
            out.push_str(digits[n as usize]);
        } else if n < 20 {
            out.push_str(teens[(n - 10) as usize]);
        } else {
            out.push_str(TENS[(n / 10) as usize]);
            if n % 10 != 0 {
                out.push_str(" ו");
                out.push_str(digits[(n % 10) as usize]);
            }
        }
    }
    if n < 10 {
        return digits[n as usize].to_owned();
    }
    if n < 20 {
        return teens[(n - 10) as usize].to_owned();
    }
    let hundred = if n >= 100 { HUNDREDS[(n / 100) as usize] } else { "" };
    let rest = n % 100;
    let suffix_len = if rest < 10 {
        digits[rest as usize].len()
    } else if rest < 20 {
        teens[(rest - 10) as usize].len()
    } else {
        TENS[(rest / 10) as usize].len()
            + if rest % 10 == 0 { 0 } else { " ו".len() + digits[(rest % 10) as usize].len() }
    };
    let mut out = String::with_capacity(
        hundred.len()
            + if hundred.is_empty() || rest == 0 { 0 } else { " ו".len() }
            + if rest == 0 && !hundred.is_empty() { 0 } else { suffix_len },
    );
    out.push_str(hundred);
    if rest != 0 || hundred.is_empty() {
        if !hundred.is_empty() {
            out.push_str(" ו");
        }
        append_under_100(&mut out, rest, digits, teens);
    }
    out
}

fn under_million(n: u32, masculine: bool) -> String {
    if n < 1000 {
        return under_thousand(n, masculine);
    }
    let k = n / 1000;
    let r = n % 1000;
    let mut s = if k <= 10 { THOUSANDS[k as usize].to_owned() } else { format!("{} אלף", under_thousand(k, true)) };
    if r != 0 {
        if r < 20 || r < 100 && r % 10 == 0 {
            s.push_str(" ו");
        } else {
            s.push(' ');
        }
        s.push_str(&under_thousand(r, masculine));
    }
    s
}

fn under_billion(n: u32, masculine: bool) -> String {
    if n < 1_000_000 {
        return under_million(n, masculine);
    }
    let m = n / 1_000_000;
    let r = n % 1_000_000;
    let mut s = match m {
        1 => "מיליון".to_owned(),
        2 => "שני מיליון".to_owned(),
        _ => format!("{} מיליון", under_thousand(m, true)),
    };
    if r != 0 {
        if r < 20 || r < 100 && r % 10 == 0 {
            s.push_str(" ו");
        } else {
            s.push(' ');
        }
        s.push_str(&under_million(r, masculine));
    }
    s
}

/// Spell a canonical, ungrouped source cardinal (zero through 999,999,999,999).
pub(crate) fn cardinal(input: &str, masculine: bool) -> Option<String> {
    if input.is_empty()
        || !input.bytes().all(|b| b.is_ascii_digit())
        || (input.len() > 1 && input.starts_with('0'))
        || input.len() > 12
    {
        return None;
    }
    let n: u64 = input.parse().ok()?;
    if n < 1_000_000_000 {
        return Some(under_billion(n as u32, masculine));
    }
    let b = (n / 1_000_000_000) as u32;
    let r = (n % 1_000_000_000) as u32;
    let mut s = match b {
        1 => "מיליארד".to_owned(),
        2 => "שני מיליארד".to_owned(),
        _ => format!("{} מיליארד", under_thousand(b, true)),
    };
    if r != 0 {
        if r < 20 || r < 100 && r % 10 == 0 {
            s.push_str(" ו");
        } else {
            s.push(' ');
        }
        s.push_str(&under_billion(r, masculine));
    }
    Some(s)
}

/// Spell a source `base_num` including properly grouped commas and feminine leading-zero sequences.
pub(crate) fn base_num(input: &str, masculine: bool) -> Option<String> {
    if input.contains(',') {
        let mut parts = input.split(',');
        let first = parts.next()?;
        if first.is_empty() || first.len() > 3 || !first.bytes().all(|b| b.is_ascii_digit()) {
            return None;
        }
        let mut digits = first.to_owned();
        for part in parts {
            if part.len() != 3 || !part.bytes().all(|b| b.is_ascii_digit()) {
                return None;
            }
            digits.push_str(part);
        }
        return cardinal(&digits, masculine);
    }
    if !masculine && input.len() > 1 && input.starts_with('0') && input.bytes().all(|b| b.is_ascii_digit()) {
        return Some(input.bytes().map(|b| FEM[(b - b'0') as usize]).collect::<Vec<_>>().join(" "));
    }
    cardinal(input, masculine)
}

/// Validate `base_num` without constructing a spoken form.
fn accepts_base(input: &str) -> bool {
    if input.contains(',') {
        let mut parts = input.split(',');
        let first = parts.next().unwrap_or("");
        if first.is_empty() || first.len() > 3 || first.starts_with('0') || !first.bytes().all(|b| b.is_ascii_digit()) {
            return false;
        }
        let mut len = first.len();
        for part in parts {
            if part.len() != 3 || !part.bytes().all(|b| b.is_ascii_digit()) {
                return false;
            }
            len += 3;
        }
        return len <= 12;
    }
    !input.is_empty() && input.bytes().all(|b| b.is_ascii_digit()) && (input.starts_with('0') || input.len() <= 12)
}

/// Enumerate the same cardinal paths and costs as `readings` without rendering.
pub(crate) fn weights(s: &str, mut push: impl FnMut(i64)) {
    if accepts_base(s) {
        push(2_050_000);
    }
    if let Some(n) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if accepts_base(n) {
            push(1_950_000);
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־' | ':') && accepts_base(&s[..i]) && accepts_base(&s[i + c.len_utf8()..])
        {
            push(if c == ':' { 3_050_000 } else { 2_000_000 });
        }
    }
    if s.contains(',') && s.split(',').all(accepts_base) {
        push(2_250_000);
    }
}

/// Materialize exactly one previously recognized source alternative.
pub(crate) fn name_at(s: &str, mut rank: usize) -> Option<String> {
    if accepts_base(s) {
        if rank == 0 {
            return base_num(s, false);
        }
        rank -= 1;
    }
    if let Some(n) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if accepts_base(n) {
            if rank == 0 {
                return Some(format!("מינוס {}", base_num(n, false)?));
            }
            rank -= 1;
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־' | ':') && accepts_base(&s[..i]) && accepts_base(&s[i + c.len_utf8()..])
        {
            if rank == 0 {
                let a = base_num(&s[..i], false)?;
                let b = base_num(&s[i + c.len_utf8()..], false)?;
                return Some(if c == ':' { format!("{a} ל{b}") } else { format!("{a} עד {b}") });
            }
            rank -= 1;
        }
    }
    if rank == 0 && s.contains(',') && s.split(',').all(accepts_base) {
        let parts: Option<Vec<_>> = s.split(',').map(|p| base_num(p, false)).collect();
        return Some(parts?.join(", "));
    }
    None
}

/// The cardinal union, including classifier and internal source weights.
pub(crate) fn readings(s: &str) -> Vec<(String, i64)> {
    let mut out = Vec::new();
    if let Some(n) = base_num(s, false) {
        out.push((n, 2_050_000));
    }
    if let Some(n) = s.strip_prefix('-').or_else(|| s.strip_prefix('−')) {
        if let Some(n) = base_num(n, false) {
            out.push((format!("מינוס {n}"), 1_950_000));
        }
    }
    for (i, c) in s.char_indices() {
        if matches!(c, '-' | '–' | '—' | '־' | ':') {
            if let (Some(a), Some(b)) = (base_num(&s[..i], false), base_num(&s[i + c.len_utf8()..], false)) {
                if c == ':' {
                    out.push((format!("{a} ל{b}"), 3_050_000));
                } else {
                    out.push((format!("{a} עד {b}"), 2_000_000));
                }
            }
        }
    }
    if s.contains(',') {
        let parts: Option<Vec<_>> = s.split(',').map(|p| base_num(p, false)).collect();
        if let Some(parts) = parts {
            if parts.len() >= 2 {
                out.push((parts.join(", "), 2_250_000));
            }
        }
    }
    out
}

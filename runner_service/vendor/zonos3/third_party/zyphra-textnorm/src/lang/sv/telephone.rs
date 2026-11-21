//! Swedish telephone, IP address and SSN classifier.

use super::number::{cardinal, spell_digits};
use crate::text::{cost, is_nemo_space};

crate::tsv!(SPECIAL_NUMBERS, "sv/telephone/special_numbers.tsv");
crate::tsv!(TELEPHONE_PROMPT, "sv/telephone/telephone_prompt.tsv");
crate::tsv!(TELEPHONE_ABBR, "sv/telephone/telephone_abbr.tsv");
crate::tsv!(IP_PROMPT, "sv/telephone/ip_prompt.tsv");
crate::tsv!(SSN_PROMPT, "sv/telephone/ssn_prompt.tsv");

#[derive(Clone, Copy)]
struct Part<'a> {
    digits: &'a [char],
    kind: u8,
}
const EMPTY: Part<'static> = Part { digits: &[], kind: 0 };

fn short(s: &[char]) -> bool {
    !s.is_empty()
        && s.len() <= 3
        && s.iter().all(char::is_ascii_digit)
        && s.iter().any(|c| *c != '0')
        && !(s.len() == 3 && s[0] == '0' && s[1] == '0' && s[2] == '0')
}
fn group(s: &[char], n: usize) -> bool {
    if s.len() < n {
        return false;
    }
    let s = &s[..n];
    match n {
        1..=3 => short(s),
        4 => short(&s[..2]) && short(&s[2..]),
        5..=8 => s.iter().all(char::is_ascii_digit),
        _ => false,
    }
}
fn render_part(p: Part<'_>) -> String {
    if p.kind == 1 {
        return spell_digits(&p.digits.iter().collect::<String>());
    }
    if p.digits.len() == 4 {
        return format!(
            "{} {}",
            render_part(Part { digits: &p.digits[..2], kind: 0 }),
            render_part(Part { digits: &p.digits[2..], kind: 0 })
        );
    }
    let mut zeros = 0;
    while zeros + 1 < p.digits.len() && p.digits[zeros] == '0' {
        zeros += 1;
    }
    let digits: String = p.digits[zeros..].iter().collect();
    let mut out = "noll ".repeat(zeros);
    out.push_str(&cardinal(&digits, false, false).unwrap_or_default());
    out
}
#[derive(Clone, Copy)]
struct Path<'a> {
    prompt: Option<&'a str>,
    prompt2: Option<&'a str>,
    prompt_trailing: bool,
    code: Option<Part<'a>>,
    plus: bool,
    parts: [Part<'a>; 8],
    count: usize,
    separator: &'static str,
    area_zero: bool,
    extension: Option<Part<'a>>,
    special: Option<&'a str>,
}
impl<'a> Path<'a> {
    fn new() -> Self {
        Self {
            prompt: None,
            prompt2: None,
            prompt_trailing: false,
            code: None,
            plus: false,
            parts: [EMPTY; 8],
            count: 0,
            separator: " ",
            area_zero: false,
            extension: None,
            special: None,
        }
    }
    fn render(self) -> String {
        let mut tag = String::from("telephone { ");
        if self.prompt.is_some() || self.code.is_some() {
            tag.push_str("country_code: \"");
            if let Some(p) = self.prompt {
                tag.push_str(p);
            }
            if let Some(p) = self.prompt2 {
                tag.push(' ');
                tag.push_str(p);
            }
            if self.prompt_trailing {
                tag.push(' ');
            }
            if let Some(code) = self.code {
                if self.prompt.is_some() {
                    tag.push(' ');
                }
                if self.plus {
                    tag.push_str("plus ");
                }
                tag.push_str(&render_part(code));
            }
            tag.push_str("\" ");
        }
        tag.push_str("number_part: \"");
        if let Some(s) = self.special {
            tag.push_str(s);
        } else {
            for i in 0..self.count {
                if i > 0 {
                    tag.push_str(if i == 1 {
                        self.separator
                    } else if self.separator == " punkt " {
                        " punkt "
                    } else {
                        " "
                    });
                }
                if i == 0 && self.area_zero {
                    tag.push_str("noll ");
                }
                tag.push_str(&render_part(self.parts[i]));
            }
        }
        tag.push('"');
        if let Some(ext) = self.extension {
            tag.push_str(" extension: \"");
            tag.push_str(&render_part(ext));
            tag.push('"');
        }
        tag.push_str(" }");
        tag
    }
}
fn emit<'a>(text: &'a [char], end: usize, path: Path<'a>, extensions: bool, sink: &mut super::Sink<'_>) {
    sink.push(end, cost(1.1), 1100, || path.render());
    if !extensions || !text.get(end).is_some_and(|c| is_nemo_space(*c)) {
        return;
    }
    let p = end + 1;
    for word in ["ankn", "ankn.", "anknytning"] {
        if !crate::text::starts_with(text, p, word) {
            continue;
        }
        let q = p + word.chars().count();
        let mut s = q;
        loop {
            // The extension field is optional in the source, even after its prompt.
            sink.push(s, cost(1.1), 1100, || path.render());
            for n in 1..=3 {
                if text.len() >= s + n && short(&text[s..s + n]) {
                    let mut next = path;
                    next.extension = Some(Part { digits: &text[s..s + n], kind: 0 });
                    sink.push(s + n, cost(1.1), 1100, || next.render());
                }
            }
            if !text.get(s).is_some_and(|c| is_nemo_space(*c)) {
                break;
            }
            s += 1;
        }
    }
}
fn base<'a>(text: &'a [char], pos: usize, path: Path<'a>, sink: &mut super::Sink<'_>) {
    // Subscriber layouts in base_number_part; 0 denotes an inserted space.
    for layout in [
        ([3, 3, 2], [1, 1]),
        ([3, 2, 2], [1, 1]),
        ([3, 2, 2], [1, 0]),
        ([3, 2, 2], [2, 0]),
        ([2, 2, 2], [1, 1]),
        ([2, 2, 2], [1, 0]),
        ([3, 2, 0], [1, 0]),
    ] {
        let mut p = pos;
        let mut v = path;
        let mut ok = true;
        for i in 0..3 {
            let n = layout.0[i];
            if n == 0 {
                break;
            }
            if !group(&text[p..], n) {
                ok = false;
                break;
            }
            v.parts[v.count] = Part { digits: &text[p..p + n], kind: 0 };
            v.count += 1;
            p += n;
            if i < 2 && layout.0[i + 1] != 0 {
                match layout.1[i] {
                    1 if text.get(p).is_some_and(|c| is_nemo_space(*c)) => p += 1,
                    2 if text.get(p) == Some(&'-') => p += 1,
                    0 => (),
                    _ => {
                        ok = false;
                        break;
                    }
                }
            }
        }
        if ok {
            emit(text, p, v, true, sink);
        }
    }
}
fn national<'a>(text: &'a [char], pos: usize, path: Path<'a>, allow_bracket_nonzero: bool, sink: &mut super::Sink<'_>) {
    for bracket in [false, true] {
        let mut p = pos;
        if bracket {
            if text.get(p) != Some(&'(') {
                continue;
            }
            p += 1;
        }
        let mut zero = false;
        if text.get(p) == Some(&'0') {
            zero = true;
            p += 1;
        } else if !bracket && text.get(p..p + 3) == Some(&['(', '0', ')']) {
            zero = true;
            p += 3;
        }
        if !zero && (!bracket || !allow_bracket_nonzero) {
            continue;
        }
        for n in 1..=3 {
            if !group(&text[p..], n) {
                continue;
            }
            let mut q = p + n;
            if bracket {
                if text.get(q) != Some(&')') {
                    continue;
                }
                q += 1;
            } else if text.get(q) == Some(&'-') {
                q += 1;
            }
            let mut v = path;
            v.area_zero = zero;
            v.separator = ", ";
            v.parts[0] = Part { digits: &text[p..p + n], kind: 0 };
            v.count = 1;
            base(text, q, v, sink);
            while text.get(q).is_some_and(|c| is_nemo_space(*c)) {
                q += 1;
                base(text, q, v, sink);
            }
        }
    }
}
fn intl_rest<'a>(text: &'a [char], pos: usize, path: Path<'a>, remaining: usize, sink: &mut super::Sink<'_>) {
    for n in 1..=8 {
        if !group(&text[pos..], n) {
            continue;
        }
        let mut v = path;
        v.parts[v.count] = Part { digits: &text[pos..pos + n], kind: u8::from(n >= 5) };
        v.count += 1;
        let end = pos + n;
        emit(text, end, v, true, sink);
        if remaining > 0 && matches!(text.get(end), Some(' ' | '-' | '.')) {
            intl_rest(text, end + 1, v, remaining - 1, sink);
        }
    }
}
fn international<'a>(text: &'a [char], pos: usize, sink: &mut super::Sink<'_>) {
    if text.get(pos) != Some(&'+') {
        return;
    }
    for n in 1..=3 {
        let p = pos + 1;
        if !group(&text[p..], n) || text[p] == '0' {
            continue;
        }
        let q = p + n;
        if !matches!(text.get(q), Some(' ' | '-' | '.')) {
            continue;
        }
        let mut path = Path::new();
        path.code = Some(Part { digits: &text[p..q], kind: 0 });
        path.plus = true;
        path.separator = ", ";
        let mut first = q + 1;
        if text.get(first..first + 3) == Some(&['(', '0', ')']) {
            first += 3;
            if text.get(first) == Some(&' ') {
                first += 1;
            }
        }
        for bracket in [false, true] {
            let s = first + usize::from(bracket);
            if bracket && text.get(first) != Some(&'(') {
                continue;
            }
            for len in 1..=if bracket { 3 } else { 8 } {
                if !group(&text[s..], len) {
                    continue;
                }
                let mut end = s + len;
                if bracket {
                    if text.get(end) != Some(&')') {
                        continue;
                    }
                    end += 1;
                }
                if !matches!(text.get(end), Some(' ' | '-' | '.')) {
                    continue;
                }
                let mut v = path;
                v.parts[0] = Part { digits: &text[s..s + len], kind: u8::from(len >= 5) };
                v.count = 1;
                intl_rest(text, end + 1, v, 4, sink);
            }
        }
    }
}
fn nanp<'a>(text: &'a [char], start: usize, mut path: Path<'a>, sink: &mut super::Sink<'_>) {
    if text.get(start) != Some(&'(') || text.len() < start + 13 {
        return;
    }
    let area = start + 1;
    if text[area] == '0' || !group(&text[area..], 3) || text.get(area + 3) != Some(&')') {
        return;
    }
    let mut p = area + 4;
    if text.get(p) == Some(&' ') {
        p += 1;
    }
    if !group(&text[p..], 3) {
        return;
    }
    let sep = p + 3;
    if !matches!(text.get(sep), Some(' ' | '-')) || !group(&text[sep + 1..], 4) {
        return;
    }
    path.separator = ", ";
    path.parts[0] = Part { digits: &text[area..area + 3], kind: 0 };
    path.parts[1] = Part { digits: &text[p..p + 3], kind: 0 };
    path.parts[2] = Part { digits: &text[sep + 1..sep + 5], kind: 0 };
    path.count = 3;
    emit(text, sep + 5, path, false, sink);
}
fn ip<'a>(text: &'a [char], pos: usize, path: Path<'a>, sink: &mut super::Sink<'_>) {
    fn walk<'a>(text: &'a [char], pos: usize, mut path: Path<'a>, sink: &mut super::Sink<'_>) {
        for n in 1..=3 {
            if !group(&text[pos..], n) {
                continue;
            }
            path.parts[path.count] = Part { digits: &text[pos..pos + n], kind: 0 };
            path.count += 1;
            let end = pos + n;
            if path.count == 4 {
                emit(text, end, path, false, sink);
            } else if text.get(end) == Some(&'.') {
                walk(text, end + 1, path, sink);
            }
            path.count -= 1;
        }
    }
    let mut path = path;
    path.separator = " punkt ";
    walk(text, pos, path, sink);
}
fn ssn<'a>(text: &'a [char], pos: usize, mut path: Path<'a>, sink: &mut super::Sink<'_>) {
    if text.len() < pos + 11
        || !group(&text[pos..], 3)
        || text[pos + 3] != '-'
        || !group(&text[pos + 4..], 2)
        || text[pos + 6] != '-'
        || !text[pos + 7..pos + 11].iter().all(char::is_ascii_digit)
    {
        return;
    }
    path.separator = ", ";
    path.parts[0] = Part { digits: &text[pos..pos + 3], kind: 0 };
    path.parts[1] = Part { digits: &text[pos + 4..pos + 6], kind: 0 };
    path.parts[2] = Part { digits: &text[pos + 7..pos + 11], kind: 1 };
    path.count = 3;
    // SSN has two comma separators.
    sink.push(pos + 11, cost(1.1), 1100, || {
        let mut tag = path.render();
        let last = render_part(path.parts[2]);
        if let Some(i) = tag.rfind(&last) {
            tag.insert(i - 1, ',');
        }
        tag
    });
}
fn prompt_numbers<'a>(text: &'a [char], pos: usize, path: Path<'a>, sink: &mut super::Sink<'_>) {
    for (row, end) in SPECIAL_NUMBERS.prefix_matches(text, pos) {
        let mut v = path;
        v.special = Some(&row.value);
        emit(text, end, v, false, sink);
    }
    national(text, pos, path, true, sink);
    let mut p = pos;
    while text.get(p).is_some_and(|c| is_nemo_space(*c)) {
        p += 1;
        national(text, p, path, true, sink);
    }
    // A prompt may be followed by a numeric country code and one space.
    for plus in [false, true] {
        let s = p + usize::from(plus);
        if plus && text.get(p) != Some(&'+') {
            continue;
        }
        for n in 1..=3 {
            if !group(&text[s..], n) {
                continue;
            }
            let end = s + n;
            if !text.get(end).is_some_and(|c| is_nemo_space(*c)) {
                continue;
            }
            let mut v = path;
            v.code = Some(Part { digits: &text[s..end], kind: 0 });
            v.plus = plus;
            national(text, end + 1, v, true, sink);
        }
    }
}
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink<'_>) {
    if start >= text.len() {
        return;
    }
    let empty = Path::new();
    national(text, start, empty, false, sink);
    nanp(text, start, empty, sink);
    international(text, start, sink);
    ip(text, start, empty, sink);
    ssn(text, start, empty, sink);
    for (row, end) in IP_PROMPT.prefix_matches(text, start) {
        let mut p = end;
        while text.get(p).is_some_and(|c| is_nemo_space(*c)) {
            p += 1;
        }
        if p > end {
            let mut v = empty;
            v.prompt = Some(&row.value);
            ip(text, p, v, sink);
        }
    }
    for (row, end) in SSN_PROMPT.prefix_matches(text, start) {
        let mut p = end;
        while text.get(p).is_some_and(|c| is_nemo_space(*c)) {
            p += 1;
        }
        if p > end {
            let mut v = empty;
            v.prompt = Some(&row.value);
            ssn(text, p, v, sink);
        }
    }
    for (row, end) in TELEPHONE_PROMPT.prefix_matches(text, start) {
        let mut v = empty;
        v.prompt = Some(&row.value);
        prompt_numbers(text, end, v, sink);
        if text.get(end).is_some_and(|c| is_nemo_space(*c)) {
            for (abbr, a) in TELEPHONE_ABBR.prefix_matches(text, end + 1) {
                let mut w = v;
                w.prompt2 = Some(&abbr.value);
                prompt_numbers(text, a, w, sink);
                for pass in [": ", ":", " "] {
                    if crate::text::starts_with(text, a, pass) {
                        let mut passed = w;
                        passed.prompt_trailing = true;
                        prompt_numbers(text, a + pass.len(), passed, sink);
                    }
                }
            }
        }
    }
    for (row, end) in TELEPHONE_ABBR.prefix_matches(text, start) {
        let mut v = empty;
        v.prompt = Some(&row.value);
        prompt_numbers(text, end, v, sink);
        for pass in [": ", ":", " "] {
            if crate::text::starts_with(text, end, pass) {
                let mut passed = v;
                passed.prompt_trailing = true;
                prompt_numbers(text, end + pass.len(), passed, sink);
            }
        }
    }
    // Bare numeric country codes share the national-number graph.
    for plus in [false, true] {
        let p = start + usize::from(plus);
        if plus && text.get(start) != Some(&'+') {
            continue;
        }
        for n in 1..=3 {
            if !group(&text[p..], n) {
                continue;
            }
            let mut v = empty;
            v.code = Some(Part { digits: &text[p..p + n], kind: 0 });
            v.plus = plus;
            national(text, p + n, v, true, sink);
            if text.get(p + n).is_some_and(|c| is_nemo_space(*c)) {
                national(text, p + n + 1, v, true, sink);
            }
            for (row, end) in SPECIAL_NUMBERS.prefix_matches(text, p + n) {
                let mut w = v;
                w.special = Some(&row.value);
                emit(text, end, w, false, sink);
            }
        }
    }
}

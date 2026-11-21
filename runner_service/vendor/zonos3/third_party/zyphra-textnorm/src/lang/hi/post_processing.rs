//! Hindi punctuation post-processing (`verbalizers/post_processing.py`).
use crate::text::is_unicode_punct;

fn no_space_before(c: char) -> bool {
    let punctuation =
        (is_unicode_punct(c) && c != '[' && c != ']') || "!#%&'()*+,-./:;<=>?@^_`{|}~\"[]".contains(c) || c == '।';
    punctuation && !matches!(c, '\'' | '"' | '«' | '-' | '—' | '<' | '{' | '(' | '[')
}

pub fn apply(text: &str) -> String {
    let mut before = String::with_capacity(text.len());
    for c in text.chars() {
        if no_space_before(c) && before.ends_with(' ') {
            before.pop();
        }
        before.push(c);
    }
    let mut out = String::with_capacity(before.len());
    let mut previous = None;
    for c in before.chars() {
        if c == ' ' && previous.is_some_and(|p| matches!(p, '<' | '{' | '(' | '[')) {
            previous = Some(c);
            continue;
        }
        out.push(c);
        previous = Some(c);
    }
    out
}

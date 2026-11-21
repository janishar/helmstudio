// Copyright (c) 2026 Zyphra, Inc.
// Licensed under the Apache License, Version 2.0.
// Vietnamese post-processing: removes spaces before/after certain punctuation marks.

fn is_no_space_before(c: char) -> bool {
    matches!(c, ',' | '.' | '!' | '?' | ':' | ';' | ')' | ']' | '}')
}

fn is_no_space_after(c: char) -> bool {
    matches!(c, '(' | '[' | '{')
}

pub fn apply(input: String) -> String {
    // No rewrite is possible without one of these adjacent ASCII pairs.
    if !input.as_bytes().windows(2).any(|pair| {
        (pair[0] == b' ' && is_no_space_before(pair[1] as char))
            || (is_no_space_after(pair[0] as char) && pair[1] == b' ')
    }) {
        return input;
    }
    let mut out = String::with_capacity(input.len());
    let mut chars = input.chars().peekable();
    // This is the preceding character in the first rewrite's output, even
    // when the second rewrite deletes it. The two rewrites each delete at
    // most one space at their respective position.
    let mut previous = None;
    while let Some(c) = chars.next() {
        if c == ' ' && chars.peek().copied().is_some_and(is_no_space_before) {
            continue;
        }
        let remove = c == ' ' && previous.is_some_and(is_no_space_after);
        previous = Some(c);
        if !remove {
            out.push(c);
        }
    }
    out
}

//! `post_process_punct(input, normalized_text)` from NeMo's `data_loader_utils.py`: re-aligns the spacing around
//! every ASCII punctuation mark of the normalized text with the original input.

use crate::text::ASCII_PUNCT;

/// One element of the Python list `normalized_text`: the original char (or `""` after a deletion) plus the spaces
/// appended by `+= " "`.
#[derive(Clone, Copy)]
struct Item {
    ch: Option<char>,
    spaces: u32,
}

impl Item {
    /// The item as a one-character string, if it is one (Python `item == "x"`).
    fn single(self) -> Option<char> {
        match (self.ch, self.spaces) {
            (Some(c), 0) => Some(c),
            (None, 1) => Some(' '),
            _ => None,
        }
    }
}

pub fn post_process_punct(input: &str, normalized: &str) -> String {
    let replaced;
    let input = if input.contains("``") && !normalized.contains("``") {
        replaced = input.replace("``", "\"");
        replaced.as_str()
    } else {
        input
    };
    // `[x for x in string.punctuation if x in input]` as a bitmask (every ASCII punctuation char is in
    // `string.punctuation`), restricted to the marks the normalized text holds: the first `list.index` of any other
    // mark raises ValueError, which skips it before any edit.
    let in_input = input.bytes().filter(u8::is_ascii_punctuation).fold(0u128, |m, b| m | 1 << b);
    let marks = normalized.bytes().filter(|&b| b < 128).fold(0u128, |m, b| m | 1 << b) & in_input;
    // with none of them, only `re.sub(" +", " ", …)` applies
    if marks == 0 {
        return crate::text::collapse_double_spaces(normalized);
    }
    let inp: Vec<char> = input.chars().collect();
    let mut out: Vec<Item> = normalized.chars().map(|c| Item { ch: Some(c), spaces: 0 }).collect();

    // The mark's positions in `out`, collected once per mark: edits only blank spaces or pad items, so no item becomes
    // the mark later, and an occurrence that stops being one (padded) is skipped by the recheck below.
    let mut ins: Vec<usize> = Vec::new();
    let mut pos_out: Vec<usize> = Vec::new();
    for p in ASCII_PUNCT.chars().filter(|&p| marks & (1 << p as u32) != 0) {
        ins.clear();
        ins.extend(inp.iter().enumerate().filter(|&(_, &c)| c == p).map(|(i, _)| i));
        pos_out.clear();
        pos_out.extend(out.iter().enumerate().filter(|(_, it)| it.single() == Some(p)).map(|(i, _)| i));
        let equal = ins.len() == pos_out.len();
        let (mut idx_in, mut idx_out) = (0usize, 0usize);
        let (mut k_in, mut k_out) = (0usize, 0usize);
        // `while punct in input[idx_in:]` / `input.index(punct, idx_in)`
        loop {
            while k_in < ins.len() && ins[k_in] < idx_in {
                k_in += 1;
            }
            let Some(&found_in) = ins.get(k_in) else { break };
            // list.index(punct, idx_out): ValueError skips the rest of this mark (edits kept)
            while k_out < pos_out.len() && (pos_out[k_out] < idx_out || out[pos_out[k_out]].single() != Some(p)) {
                k_out += 1;
            }
            let Some(&found_out) = pos_out.get(k_out) else { break };
            idx_out = found_out;
            idx_in = found_in;

            let valid = (idx_out > 0 && idx_in > 0 && out[idx_out - 1].single() == Some(inp[idx_in - 1]))
                || (idx_out + 1 < out.len()
                    && idx_in + 1 < inp.len()
                    && out[idx_out + 1].single() == Some(inp[idx_in + 1]));
            if !equal && !valid {
                idx_in += 1;
                continue;
            }
            if idx_in > 0 && idx_out > 0 {
                let prev_is_space = out[idx_out - 1].single() == Some(' ');
                if prev_is_space && inp[idx_in - 1] != ' ' {
                    out[idx_out - 1] = Item { ch: None, spaces: 0 };
                } else if !prev_is_space && inp[idx_in - 1] == ' ' {
                    out[idx_out - 1].spaces += 1;
                }
            }
            if idx_in + 1 < inp.len() && idx_out + 1 < out.len() {
                let next_is_space = out[idx_out + 1].single() == Some(' ');
                if next_is_space && inp[idx_in + 1] != ' ' {
                    out[idx_out + 1] = Item { ch: None, spaces: 0 };
                } else if !next_is_space && inp[idx_in + 1] == ' ' {
                    out[idx_out].spaces += 1;
                }
            }
            idx_out += 1;
            idx_in += 1;
        }
    }

    // "".join(...) then re.sub(" +", " ", ...)
    let mut s = String::with_capacity(normalized.len());
    let mut prev_space = false;
    let mut push = |c: char, s: &mut String| {
        if c == ' ' {
            if !prev_space {
                s.push(' ');
            }
            prev_space = true;
        } else {
            s.push(c);
            prev_space = false;
        }
    };
    for it in &out {
        if let Some(c) = it.ch {
            push(c, &mut s);
        }
        for _ in 0..it.spaces {
            push(' ', &mut s);
        }
    }
    s
}

#[cfg(test)]
mod tests {
    use super::post_process_punct;

    #[test]
    fn moves_quote_to_match_input() {
        assert_eq!(post_process_punct("12 test' example", "twelve test 'example"), "twelve test' example");
    }

    #[test]
    fn attaches_and_detaches_spaces() {
        assert_eq!(
            post_process_punct("It costs $5. Next day.", "It costs five dollars. Next day."),
            "It costs five dollars. Next day."
        );
        assert_eq!(post_process_punct("a , b", "a, b"), "a , b");
        assert_eq!(post_process_punct("(5)", "( five )"), "(five)");
    }

    #[test]
    fn missing_mark_keeps_previous_edits() {
        // "$" is absent from the output: index() fails, the mark is skipped, other marks still processed
        assert_eq!(post_process_punct("$5, ok", "five dollars , ok"), "five dollars, ok");
    }
}

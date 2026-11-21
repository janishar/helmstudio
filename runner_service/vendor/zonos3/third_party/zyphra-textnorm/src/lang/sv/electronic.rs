//! Swedish electronic classifier: email addresses, URLs, and domains.

crate::tsv!(DOMAINS, "sv/electronic/domain.tsv");
crate::tsv!(SERVERS, "sv/electronic/server_name.tsv");
crate::tsv!(SYMBOLS, "sv/electronic/symbols.tsv");

fn alpha(c: char) -> bool {
    c.is_ascii_alphabetic() || matches!(c, 'Å' | 'Ä' | 'Ö' | 'Ü' | 'É' | 'å' | 'ä' | 'ö' | 'ü' | 'é')
}

fn label_char(c: char) -> bool {
    alpha(c) || c.is_ascii_digit() || matches!(c, '-' | '_')
}

fn symbol(c: char) -> bool {
    matches!(
        c,
        '.' | ':'
            | '-'
            | '_'
            | '!'
            | '#'
            | '$'
            | '%'
            | '&'
            | '\''
            | '*'
            | '+'
            | '/'
            | '='
            | '?'
            | '^'
            | '{'
            | '}'
            | '~'
            | ','
    )
}

fn tail_char(c: char) -> bool {
    alpha(c) || c.is_ascii_digit() || symbol(c)
}
fn tail_final(c: char) -> bool {
    alpha(c) || c.is_ascii_digit() || matches!(c, '/' | '_' | '-' | '=' | '&' | '%' | '#')
}

// url_tail: epsilon, a lone slash, or a delimiter followed by tail characters
// whose last character is in the source's non_trailing_punct alphabet.
fn tail_ends(text: &[char], start: usize, mut emit: impl FnMut(usize)) {
    emit(start);
    let Some(&first) = text.get(start) else { return };
    if !matches!(first, '/' | ':' | '?' | '#') {
        return;
    }
    if first == '/' {
        emit(start + 1);
    }
    let mut end = start + 1;
    while end < text.len() && tail_char(text[end]) {
        end += 1;
        if tail_final(text[end - 1]) {
            emit(end);
        }
    }
}

fn push_domain(
    sink: &mut super::Sink,
    rank: &mut i32,
    text: &[char],
    start: usize,
    domain_start: usize,
    end: usize,
    protocol: Option<&str>,
) {
    sink.push(start + end, crate::text::cost(1.10), 1200 + *rank, || {
        let domain: String = text[domain_start..end].iter().collect();
        let field = if let Some(protocol) = protocol {
            format!("protocol: \"{protocol}\" domain: \"{domain}\"")
        } else {
            format!("domain: \"{domain}\"")
        };
        format!("electronic {{ {field} preserve_order: true }}")
    });
    *rank += 1;
}

fn protocol_domains(
    sink: &mut super::Sink,
    rank: &mut i32,
    text: &[char],
    start: usize,
    domain_start: usize,
    protocol: &str,
    http: bool,
) {
    let mut pos = domain_start;
    let mut label_len = 0;
    let mut dotted = false;
    let mut first_has_alpha = false;
    while pos < text.len() {
        let c = text[pos];
        if label_char(c) {
            if !dotted {
                first_has_alpha |= alpha(c);
            }
            label_len += 1;
            pos += 1;
            if dotted {
                tail_ends(text, pos, |end| push_domain(sink, rank, text, start, domain_start, end, Some(protocol)));
            } else if http && first_has_alpha {
                // local_or_port_host: a port or slash is mandatory.
                if text.get(pos) == Some(&':') {
                    let mut port_end = pos + 1;
                    while port_end < text.len() && text[port_end].is_ascii_digit() {
                        port_end += 1;
                        tail_ends(text, port_end, |end| {
                            push_domain(sink, rank, text, start, domain_start, end, Some(protocol))
                        });
                    }
                }
                if text.get(pos) == Some(&'/') {
                    tail_ends(text, pos, |end| {
                        if end > pos {
                            push_domain(sink, rank, text, start, domain_start, end, Some(protocol));
                        }
                    });
                }
            }
        } else if c == '.' && label_len > 0 {
            dotted = true;
            label_len = 0;
            pos += 1;
        } else {
            break;
        }
    }
}

/// Enumerate every accepted electronic prefix and its original tag fields.
pub(super) fn classify(text: &[char], start: usize, sink: &mut super::Sink) {
    let rem = &text[start..];
    let mut rank = 0;
    for (written, http) in
        [("https://www.", true), ("http://www.", true), ("https://", true), ("http://", true), ("www.", false)]
    {
        let len = written.chars().count();
        if rem.iter().copied().take(len).eq(written.chars()) {
            protocol_domains(sink, &mut rank, rem, start, len, written, http);
        }
    }

    // Email username is any nonempty run of alpha, digits, and symbols.
    let mut user_end = 0;
    while user_end < rem.len() && (alpha(rem[user_end]) || rem[user_end].is_ascii_digit() || symbol(rem[user_end])) {
        user_end += 1;
    }
    if user_end > 0 && rem.get(user_end) == Some(&'@') {
        let at = user_end + 1;
        if rem.get(at).is_some_and(|&c| alpha(c) || c.is_ascii_digit()) {
            let mut pos = at + 1;
            while pos < rem.len() && label_char(rem[pos]) {
                pos += 1;
            }
            while rem.get(pos) == Some(&'.') && rem.get(pos + 1).is_some_and(|&c| label_char(c)) {
                pos += 1;
                while pos < rem.len() && label_char(rem[pos]) {
                    pos += 1;
                    sink.push(start + pos, crate::text::cost(1.10), 1200 + rank, || {
                        let username: String = rem[..user_end].iter().collect();
                        let domain: String = rem[at..pos].iter().collect();
                        format!("electronic {{ username: \"{username}\" domain: \"{domain}\" preserve_order: true }}")
                    });
                    rank += 1;
                }
            }
        }
    }

    // A bare host's first label must contain a letter. Each remaining label
    // is nonempty, and the final domain suffix comes from the source table.
    let mut pos = 0;
    let mut label_len = 0;
    let mut first_has_alpha = false;
    let mut first_label = true;
    while pos < rem.len() {
        let c = rem[pos];
        if label_char(c) {
            if first_label {
                first_has_alpha |= alpha(c);
            }
            label_len += 1;
            pos += 1;
        } else if c == '.' && label_len > 0 {
            if first_has_alpha {
                for (_, end) in DOMAINS.prefix_matches(rem, pos) {
                    tail_ends(rem, end, |tail_end| push_domain(sink, &mut rank, rem, start, 0, tail_end, None));
                }
            }
            first_label = false;
            label_len = 0;
            pos += 1;
        } else {
            break;
        }
    }
}

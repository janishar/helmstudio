// Modern Greek cardinal and ordinal morphology, following el/taggers/cardinal.py
// and el/taggers/ordinal.py in the production NeMo grammar.
const U: [&str; 10] = ["μηδέν", "ένα", "δύο", "τρία", "τέσσερα", "πέντε", "έξι", "επτά", "οκτώ", "εννέα"];
const TEEN: [&str; 10] = [
    "δέκα",
    "έντεκα",
    "δώδεκα",
    "δεκατρία",
    "δεκατέσσερα",
    "δεκαπέντε",
    "δεκαέξι",
    "δεκαεπτά",
    "δεκαοκτώ",
    "δεκαεννέα",
];
const TENS: [&str; 10] =
    ["", "", "είκοσι", "τριάντα", "σαράντα", "πενήντα", "εξήντα", "εβδομήντα", "ογδόντα", "ενενήντα"];
const HUNDREDS: [&str; 10] = [
    "",
    "εκατό",
    "διακόσια",
    "τριακόσια",
    "τετρακόσια",
    "πεντακόσια",
    "εξακόσια",
    "επτακόσια",
    "οκτακόσια",
    "εννιακόσια",
];

pub fn digits(s: &str) -> Option<String> {
    if !s.bytes().all(|c| c.is_ascii_digit()) {
        return None;
    }
    if !super::writing_tags() {
        return Some(String::new());
    }
    let mut out = String::with_capacity(s.len() * 16);
    for digit in s.bytes() {
        append(&mut out, U[(digit - b'0') as usize]);
    }
    Some(out)
}

#[inline]
fn append(out: &mut String, word: &str) {
    if !out.is_empty() {
        out.push(' ');
    }
    out.push_str(word);
}

fn under_1000(out: &mut String, n: usize, feminine: bool) {
    let h = n / 100;
    let r = n % 100;
    if h > 0 {
        let hundred = if h == 1 && r != 0 {
            "εκατόν"
        } else if feminine && h > 1 {
            [
                "",
                "",
                "διακόσιες",
                "τριακόσιες",
                "τετρακόσιες",
                "πεντακόσιες",
                "εξακόσιες",
                "επτακόσιες",
                "οκτακόσιες",
                "εννιακόσιες",
            ][h]
        } else {
            HUNDREDS[h]
        };
        append(out, hundred);
    }
    if r >= 20 {
        append(out, TENS[r / 10]);
    }
    if (10..20).contains(&r) {
        append(
            out,
            if feminine && r == 13 {
                "δεκατρείς"
            } else if feminine && r == 14 {
                "δεκατέσσερις"
            } else {
                TEEN[r - 10]
            },
        );
    } else if r % 10 != 0 || n == 0 {
        let u = r % 10;
        append(
            out,
            if feminine {
                match u {
                    1 => "μία",
                    3 => "τρεις",
                    4 => "τέσσερις",
                    _ => U[u],
                }
            } else {
                U[u]
            },
        );
    }
}

pub fn cardinal(s: &str) -> Option<String> {
    if s.is_empty() || s.len() > 15 || !s.bytes().all(|c| c.is_ascii_digit()) || (s.len() > 1 && s.starts_with('0')) {
        return None;
    }
    if !super::writing_tags() {
        return Some(String::new());
    }
    let n: u64 = s.parse().ok()?;
    if n == 0 {
        return Some(U[0].into());
    }
    let mut out = String::with_capacity(s.len() * 16);
    let groups = [
        (1_000_000_000_000u64, "τρισεκατομμύριο", "τρισεκατομμύρια"),
        (1_000_000_000, "δισεκατομμύριο", "δισεκατομμύρια"),
        (1_000_000, "εκατομμύριο", "εκατομμύρια"),
        (1000, "χίλια", "χιλιάδες"),
    ];
    let mut rem = n;
    for (scale, singular, plural) in groups {
        let count = (rem / scale) as usize;
        rem %= scale;
        if count == 0 {
            continue;
        }
        if count == 1 {
            if scale == 1000 {
                append(&mut out, singular);
            } else {
                append(&mut out, "ένα");
                append(&mut out, singular);
            }
        } else {
            under_1000(&mut out, count, scale == 1000);
            append(&mut out, plural);
        }
    }
    if rem > 0 {
        under_1000(&mut out, rem as usize, false);
    }
    Some(out)
}

pub fn agree(s: &str, masculine: bool) -> String {
    if !super::writing_tags() {
        return String::new();
    }
    let boundary = s
        .split(' ')
        .enumerate()
        .filter_map(|(i, w)| {
            matches!(
                w,
                "χιλιάδες"
                    | "εκατομμύριο"
                    | "εκατομμύρια"
                    | "δισεκατομμύριο"
                    | "δισεκατομμύρια"
                    | "τρισεκατομμύριο"
                    | "τρισεκατομμύρια"
            )
            .then_some(i + 1)
        })
        .last()
        .unwrap_or(0);
    let mut out = String::with_capacity(s.len() + 8);
    for (i, w) in s.split(' ').enumerate() {
        if i != 0 {
            out.push(' ');
        }
        let mapped = if i < boundary {
            w
        } else {
            match w {
                "ένα" => {
                    if masculine {
                        "ένας"
                    } else {
                        "μία"
                    }
                }
                "τρία" => "τρεις",
                "τέσσερα" => "τέσσερις",
                "δεκατρία" => "δεκατρείς",
                "δεκατέσσερα" => "δεκατέσσερις",
                "διακόσια" => {
                    if masculine {
                        "διακόσιοι"
                    } else {
                        "διακόσιες"
                    }
                }
                "τριακόσια" => {
                    if masculine {
                        "τριακόσιοι"
                    } else {
                        "τριακόσιες"
                    }
                }
                "τετρακόσια" => {
                    if masculine {
                        "τετρακόσιοι"
                    } else {
                        "τετρακόσιες"
                    }
                }
                "πεντακόσια" => {
                    if masculine {
                        "πεντακόσιοι"
                    } else {
                        "πεντακόσιες"
                    }
                }
                "εξακόσια" => {
                    if masculine {
                        "εξακόσιοι"
                    } else {
                        "εξακόσιες"
                    }
                }
                "επτακόσια" => {
                    if masculine {
                        "επτακόσιοι"
                    } else {
                        "επτακόσιες"
                    }
                }
                "οκτακόσια" => {
                    if masculine {
                        "οκτακόσιοι"
                    } else {
                        "οκτακόσιες"
                    }
                }
                "εννιακόσια" => {
                    if masculine {
                        "εννιακόσιοι"
                    } else {
                        "εννιακόσιες"
                    }
                }
                "χίλια" => {
                    if masculine {
                        "χίλιοι"
                    } else {
                        "χίλιες"
                    }
                }
                x => x,
            }
        };
        out.push_str(mapped);
    }
    out
}

pub fn ordinal(n: usize, case: &str) -> Option<String> {
    let stems = [
        "",
        "πρώτ",
        "δεύτερ",
        "τρίτ",
        "τέταρτ",
        "πέμπτ",
        "έκτ",
        "έβδομ",
        "όγδο",
        "ένατ",
        "δέκατ",
        "ενδέκατ",
        "δωδέκατ",
    ];
    let tens = [
        "",
        "",
        "εικοστ",
        "τριακοστ",
        "τεσσαρακοστ",
        "πεντηκοστ",
        "εξηκοστ",
        "εβδομηκοστ",
        "ογδοηκοστ",
        "ενενηκοστ",
        "εκατοστ",
    ];
    let (plain, accented) = match case {
        "ος" => ("ος", "ός"),
        "η" => ("η", "ή"),
        "ο" => ("ο", "ό"),
        "ου" => ("ου", "ού"),
        "ης" => ("ης", "ής"),
        "οι" => ("οι", "οί"),
        "ες" => ("ες", "ές"),
        "α" => ("α", "ά"),
        "ων" => ("ων", "ών"),
        "ους" => ("ους", "ούς"),
        _ => return None,
    };
    if !super::writing_tags() {
        return ((1..=100).contains(&n) || (n == 1000 && matches!(case, "ος" | "η" | "ο"))).then(String::new);
    }
    if n == 1000 && matches!(case, "ος" | "η" | "ο") {
        return Some(format!("χιλιοστ{accented}"));
    }
    if n == 0 || n > 100 {
        return None;
    }
    if n <= 12 {
        return Some(format!("{}{plain}", stems[n]));
    }
    if n < 20 {
        return Some(format!("δέκατ{plain} {}{plain}", stems[n - 10]));
    }
    let t = n / 10;
    if n % 10 == 0 {
        Some(format!("{}{accented}", tens[t]))
    } else {
        Some(format!("{}{accented} {}{plain}", tens[t], stems[n % 10]))
    }
}

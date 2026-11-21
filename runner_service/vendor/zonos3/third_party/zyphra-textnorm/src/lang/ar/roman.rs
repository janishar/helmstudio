//! Arabic Roman numerals classifier.
//! Matching `ar/taggers/roman.py`.

use super::cardinal::spell_cardinal;
use super::char_util::{token_end, token_start};
use crate::lang::common::Punctuation;
use crate::lattice::{Candidate, Classifier};
use crate::text::cost;
use std::collections::HashMap;
use std::sync::LazyLock;

crate::tsv!(ORDINALS, "ar/ordinal/ordinals.tsv");

const MASC_KEYWORDS: &[&str] = &[
    "الفصل",
    "القرن",
    "الجزء",
    "المجلد",
    "الباب",
    "القسم",
    "الكتاب",
    "البند",
    "الملحق",
    "المستوى",
    "الصف",
    "الطابق",
    "الدور",
    "الموسم",
    "النوع",
    "النمط",
    "العصر",
    "الأسطول",
    "الجيش",
    "الفيلق",
    "المجمع",
    "المؤتمر",
    "العهد",
    "الجيل",
    "لويس",
    "هنري",
    "تشارلز",
    "شارل",
    "فيليب",
    "جورج",
    "إدوارد",
    "ويليام",
    "وليام",
    "فيلهلم",
    "ريتشارد",
    "جيمس",
    "فرانسيس",
    "فرنسيس",
    "بندكت",
    "بنديكت",
    "بندكتس",
    "يوحنا",
    "يوحنا بولس",
    "بولس",
    "بيوس",
    "ليو",
    "لاون",
    "غريغوري",
    "غريغوريوس",
    "رمسيس",
    "تحتمس",
    "أمنحتب",
    "بطليموس",
    "محمد",
    "الحسن",
    "عبد الله",
    "قابوس",
    "فؤاد",
    "فاروق",
    "فيصل",
    "حسين",
    "سليمان",
    "سليم",
    "مراد",
    "بايزيد",
    "عبد الحميد",
    "بطرس",
    "إيفان",
    "نيكولاس",
    "نيقولا",
    "ألكسندر",
    "الإسكندر",
    "نابليون",
    "قسطنطين",
    "كارل",
    "خوان كارلوس",
    "فيليبي",
    "الملك",
    "البابا",
    "السلطان",
    "الإمبراطور",
    "القيصر",
    "الخليفة",
    "الأمير",
];

const FEM_KEYWORDS: &[&str] = &[
    "المادة",
    "الفقرة",
    "المرحلة",
    "الطبعة",
    "الحرب العالمية",
    "الحلقة",
    "المجموعة",
    "الدرجة",
    "الآية",
    "السورة",
    "الفرقة",
    "السيمفونية",
    "السمفونية",
    "الجمهورية",
    "إليزابيث",
    "اليزابيث",
    "كاثرين",
    "كاترين",
    "ماري",
    "ماريا",
    "فيكتوريا",
    "مارغريت",
    "مارغريتا",
    "آن",
    "آنا",
    "كليوباترا",
    "الملكة",
];

const INDEF_KEYWORDS: &[&str] =
    &["فصل", "جزء", "مجلد", "باب", "قسم", "ملحق", "بند", "فقرة", "مادة", "طبعة", "رقم", "قرن"];

// Keep source order, including overlapping and multi-word keywords.
static KEYWORDS: LazyLock<HashMap<char, Vec<(Vec<char>, i32)>>> = LazyLock::new(|| {
    let mut index = HashMap::<char, Vec<(Vec<char>, i32)>>::new();
    for (list, kind) in [(MASC_KEYWORDS, 0), (FEM_KEYWORDS, 1), (INDEF_KEYWORDS, 2)] {
        for &keyword in list {
            let chars: Vec<char> = keyword.chars().collect();
            if let Some(&first) = chars.first() {
                index.entry(first).or_default().push((chars, kind));
            }
        }
    }
    index
});

fn parse_roman(chars: impl Iterator<Item = char>) -> Option<u32> {
    let mut val = 0;
    let mut prev = 0;
    for c in chars {
        let v = match c {
            'I' => 1,
            'V' => 5,
            'X' => 10,
            'L' => 50,
            'C' => 100,
            'D' => 500,
            'M' => 1000,
            _ => return None,
        };
        if v > prev {
            val += v - 2 * prev;
        } else {
            val += v;
        }
        prev = v;
    }
    if val > 0 && val < 1000 { Some(val) } else { None }
}

pub fn fem_ordinal(n: u32) -> String {
    let ones_1_10 = [
        "",
        "الأولى",
        "الثانية",
        "الثالثة",
        "الرابعة",
        "الخامسة",
        "السادسة",
        "السابعة",
        "الثامنة",
        "التاسعة",
        "العاشرة",
    ];
    let compound_ones =
        ["", "الحادية", "الثانية", "الثالثة", "الرابعة", "الخامسة", "السادسة", "السابعة", "الثامنة", "التاسعة"];
    let tens = ["", "", "العشرون", "الثلاثون", "الأربعون", "الخمسون", "الستون", "السبعون", "الثمانون", "التسعون"];

    if n <= 10 {
        return ones_1_10[n as usize].to_string();
    }
    if n < 20 {
        return format!("{} عشرة", compound_ones[(n - 10) as usize]);
    }
    let t = (n / 10) as usize;
    let u = (n % 10) as usize;
    if u == 0 { tens[t].to_string() } else { format!("{} و{}", compound_ones[u], tens[t]) }
}

pub struct RomanClassifier<'a> {
    pub punct: &'a Punctuation,
}

impl Classifier for RomanClassifier<'_> {
    fn candidates(&self, text: &[char], start: usize, out: &mut Vec<Candidate>) {
        if !token_start(text, start, self.punct) {
            return;
        }

        let Some(keywords) = text.get(start).and_then(|ch| KEYWORDS.get(ch)) else { return };
        for (keyword, kind) in keywords {
            let r_idx = start + keyword.len();
            if r_idx >= text.len() || text[start..r_idx] != keyword[..] || text[r_idx] != ' ' {
                continue;
            }
            let mut end = r_idx + 1;
            while end < text.len() && matches!(text[end], 'I' | 'V' | 'X' | 'L' | 'C' | 'D' | 'M') {
                end += 1;
            }
            if end > r_idx + 1 && token_end(text, end, self.punct) {
                if let Some(val) = parse_roman(text[r_idx + 1..end].iter().copied()) {
                    if *kind != 1 || val < 100 {
                        out.push(Candidate { end, cost: cost(1.05), rank: *kind, tag: String::new() });
                    }
                }
            }
        }
    }

    fn materialize(&self, text: &[char], start: usize, candidate: &mut Candidate) {
        let mut sp_pos = start;
        while sp_pos < candidate.end && text[sp_pos] != ' ' {
            sp_pos += 1;
        }
        let kw: String = text[start..sp_pos].iter().collect();
        let val = parse_roman(text[sp_pos + 1..candidate.end].iter().copied()).unwrap_or(1);

        let spoken_num = match candidate.rank {
            0 => {
                let key = val.to_string();
                ORDINALS.rows().iter().find(|r| r.key == key).map_or("الأول", |r| r.value.as_str()).to_string()
            }
            1 => fem_ordinal(val),
            _ => spell_cardinal(val as u64),
        };

        let full_spoken = format!("{kw} {spoken_num}").replace(' ', "\u{a0}");
        candidate.tag = format!("name: \"{full_spoken}\"");
    }
}

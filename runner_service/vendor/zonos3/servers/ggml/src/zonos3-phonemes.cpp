// Port of zonos3/tts/phonemes.py; see zonos3-phonemes.h.
#include "zonos3-phonemes.h"
#include "zonos3-languages.h"
#include "zonos3-text.h"
#include "unicode-data.h"

#include "json.hpp"
#include "lmdb.h"

#include <algorithm>
#include <cstdio>
#include <cstdlib>
#include <cstring>
#include <set>
#include <stdexcept>

#ifdef _WIN32
#define WIN32_LEAN_AND_MEAN
#define NOMINMAX
#include <windows.h>
#else
#include <dlfcn.h>
#endif

namespace {

using u32 = std::u32string;

constexpr int LEXICON_VERSION = 1;
constexpr size_t MAX_KEY_BYTES = 500; // LMDB key limit; the builder skips longer headwords
constexpr size_t CJK_MAX = 8;         // longest headword tried by maximum matching
constexpr size_t TIES_SAMPLE = 20000; // uses_ties: values inspected
constexpr char32_t TIE = U'\u0361';

// ---------------------------------------------------------------- Unicode data (unicode-data.h)

template <size_t N, size_t W>
const uint32_t * find_range(const uint32_t (&table)[N][W], uint32_t cp) {
    size_t lo = 0, hi = N;
    while (lo < hi) {
        const size_t mid = (lo + hi) / 2;
        if (table[mid][1] < cp) lo = mid + 1;
        else hi = mid;
    }
    return lo < N && table[lo][0] <= cp ? table[lo] : nullptr;
}
template <size_t N, size_t W>
const uint32_t * find_key(const uint32_t (&table)[N][W], uint32_t cp) {
    size_t lo = 0, hi = N;
    while (lo < hi) {
        const size_t mid = (lo + hi) / 2;
        if (table[mid][0] < cp) lo = mid + 1;
        else hi = mid;
    }
    return lo < N && table[lo][0] == cp ? table[lo] : nullptr;
}

bool is_letter(char32_t c) {
    return find_range(ZONOS3_UNI_L, c) != nullptr;
}
bool is_mark(char32_t c) {
    return find_range(ZONOS3_UNI_M, c) != nullptr;
}
bool is_space(char32_t c) {
    return find_range(ZONOS3_UNI_SPACE, c) != nullptr;
}
uint32_t ccc(char32_t c) {
    const uint32_t * r = find_range(ZONOS3_UNI_CCC, c);
    return r ? r[2] : 0;
}

// Hangul syllable arithmetic (Unicode 3.12).
constexpr char32_t S_BASE = 0xAC00, L_BASE = 0x1100, V_BASE = 0x1161, T_BASE = 0x11A7;
constexpr char32_t L_COUNT = 19, V_COUNT = 21, T_COUNT = 28, N_COUNT = V_COUNT * T_COUNT, S_COUNT = L_COUNT * N_COUNT;

void decompose(char32_t c, u32 & out) {
    if (c >= S_BASE && c < S_BASE + S_COUNT) {
        const char32_t s = c - S_BASE;
        out.push_back(L_BASE + s / N_COUNT);
        out.push_back(V_BASE + (s % N_COUNT) / T_COUNT);
        if (s % T_COUNT) out.push_back(T_BASE + s % T_COUNT);
        return;
    }
    if (const uint32_t * d = find_key(ZONOS3_UNI_DECOMP, c)) {
        decompose(d[1], out);
        if (d[2]) decompose(d[2], out);
        return;
    }
    out.push_back(c);
}

char32_t compose(char32_t a, char32_t b) {
    if (a >= L_BASE && a < L_BASE + L_COUNT && b >= V_BASE && b < V_BASE + V_COUNT)
        return S_BASE + ((a - L_BASE) * V_COUNT + (b - V_BASE)) * T_COUNT;
    if (a >= S_BASE && a < S_BASE + S_COUNT && (a - S_BASE) % T_COUNT == 0 && b > T_BASE && b < T_BASE + T_COUNT)
        return a + (b - T_BASE);
    size_t lo = 0, hi = sizeof(ZONOS3_UNI_COMPOSE) / sizeof(ZONOS3_UNI_COMPOSE[0]);
    while (lo < hi) {
        const size_t mid = (lo + hi) / 2;
        const uint32_t * r = ZONOS3_UNI_COMPOSE[mid];
        if (r[0] < a || (r[0] == a && r[1] < b)) lo = mid + 1;
        else hi = mid;
    }
    const uint32_t * r = ZONOS3_UNI_COMPOSE[lo < sizeof(ZONOS3_UNI_COMPOSE) / sizeof(ZONOS3_UNI_COMPOSE[0]) ? lo : 0];
    return r[0] == a && r[1] == b ? r[2] : 0;
}

// unicodedata.normalize("NFC", s): canonical decomposition, canonical ordering, canonical composition.
u32 nfc(const u32 & s) {
    u32 d;
    d.reserve(s.size());
    for (char32_t c : s) decompose(c, d);
    for (size_t i = 0; i < d.size();) { // stable sort of each run of non-starters
        if (ccc(d[i]) == 0) {
            ++i;
            continue;
        }
        size_t j = i;
        while (j < d.size() && ccc(d[j]) != 0) ++j;
        std::stable_sort(d.begin() + i, d.begin() + j, [](char32_t x, char32_t y) { return ccc(x) < ccc(y); });
        i = j;
    }
    u32 out;
    out.reserve(d.size());
    long starter = -1;
    uint32_t last_cc = 0; // class of the last character kept after the starter
    for (char32_t c : d) {
        const uint32_t cc = ccc(c);
        if (starter >= 0) {
            const bool adjacent = (long) out.size() - 1 == starter;
            if (adjacent || (last_cc != 0 && last_cc < cc)) { // not blocked
                if (char32_t p = compose(out[starter], c)) {
                    out[starter] = p;
                    continue;
                }
            }
        }
        if (cc == 0) starter = (long) out.size();
        last_cc = cc;
        out.push_back(c);
    }
    return out;
}

// str.casefold()
u32 casefold(const u32 & s) {
    u32 out;
    out.reserve(s.size());
    for (char32_t c : s) {
        if (const uint32_t * f = find_key(ZONOS3_UNI_CASEFOLD, c)) out.append(f + 2, f + 2 + f[1]);
        else out.push_back(c);
    }
    return out;
}

u32 normalize_headword(const u32 & word) {
    size_t a = 0, b = word.size(); // str.strip()
    while (a < b && is_space(word[a])) ++a;
    while (b > a && is_space(word[b - 1])) --b;
    u32 s = nfc(word.substr(a, b - a));
    std::replace(s.begin(), s.end(), U'\u2019', U'\'');
    return casefold(s);
}

// phonemes.py _char_class: "" for non-letters.
enum char_cls { NONE = 0,
                HIRA = ZONOS3_UNI_HIRA,
                KATA = ZONOS3_UNI_KATA,
                HANGUL = ZONOS3_UNI_HANGUL,
                HAN = ZONOS3_UNI_HAN,
                LETTER = 5 };
int char_class(char32_t c) {
    if (!is_letter(c)) return NONE;
    const uint32_t * r = find_range(ZONOS3_UNI_CJK_CLASS, c);
    return r ? (int) r[2] : LETTER;
}

const char * status_of(const std::optional<std::string> & value) {
    return !value ? "unknown" : value->empty() ? "ambiguous"
                                               : "ok";
}

// ---------------------------------------------------------------- espeak-ng harmonization

void replace_all(u32 & s, const u32 & a, const u32 & b) { // str.replace
    if (a.empty()) return;
    u32 out;
    size_t pos = 0;
    for (size_t hit; (hit = s.find(a, pos)) != u32::npos; pos = hit + a.size()) {
        out.append(s, pos, hit - pos);
        out += b;
    }
    out.append(s, pos, u32::npos);
    s.swap(out);
}

// _AFFRICATE = ([td])([sʃzʒɕʑʂʐ])|(p)(f): leftmost non-overlapping matches.
bool affricate_at(const u32 & s, size_t i) {
    if (i + 1 >= s.size()) return false;
    static const u32 fric = U"s\u0283z\u0292\u0255\u0291\u0282\u0290";
    return ((s[i] == U't' || s[i] == U'd') && fric.find(s[i + 1]) != u32::npos) || (s[i] == U'p' && s[i + 1] == U'f');
}
size_t count_affricates(const u32 & s) {
    size_t n = 0;
    for (size_t i = 0; i < s.size();) {
        if (affricate_at(s, i)) {
            ++n;
            i += 2;
        } else ++i;
    }
    return n;
}

using symbol_table = std::vector<std::pair<u32, u32>>;
// espeak-ng symbols -> the lexicon's notation where the two systematically differ (_ESPEAK_SYMBOLS).
const std::map<std::string, symbol_table> & espeak_symbols() {
    static const std::map<std::string, symbol_table> table = [] {
        std::map<std::string, symbol_table> t;
        // CMUdict-derived English: no length marks, rhotic ɝ/ɚ, no flaps or glottal stops.
        t["eng"] = {
            {U"\u025C\u02D0", U"\u025D"},
            {U"\u025C", U"\u025D"},
            {U"o\u02D0\u0279", U"\u0254\u0279"},
            {U"\u02D0", U""},
            {U"\u0250", U"\u0259"},
            {U"\u1D7B", U"\u026A"},
            {U"\u027E", U"t"},
            {U"\u0294", U"t"},
            {U"n\u0329", U"\u0259n"},
            {U"l\u0329", U"\u0259l"},
            {U"m\u0329", U"\u0259m"},
            {U"\u0252", U"\u0251"},
            {U"x", U"k"},
        };
        // WikiPron German: uvular ʁ, vocalized -er as ɐ, front long a, marked diphthongs.
        t["deu"] = {
            {U"\u027E", U"\u0281"},
            {U"r", U"\u0281"},
            {U"\u025C", U"\u0250"},
            {U"\u0251\u02D0", U"a\u02D0"},
            {U"a\u028A", U"a\u028A\u032F"},
            {U"a\u026A", U"a\u026A\u032F"},
            {U"\u0254\u00F8", U"\u0254\u026A\u032F"},
        };
        // WikiPron Italian: geminates spelled double, no lax final vowels or taps.
        symbol_table ita = {
            {U"t\u0283\u02D0", U"tt\u0283"},
            {U"d\u0292\u02D0", U"dd\u0292"},
            {U"ts\u02D0", U"tts"},
            {U"dz\u02D0", U"ddz"},
        };
        for (char32_t c : u32(U"bdfklmnprstvz\u0283\u028E\u0272\u0261")) ita.push_back({u32{c, U'\u02D0'}, u32{c, c}});
        ita.push_back({U"\u026A", U"i"});
        ita.push_back({U"\u028A", U"u"});
        ita.push_back({U"\u027E", U"r"});
        t["ita"] = ita;
        // WikiPron Russian: retroflex ш/ж, ɨ for ы, plain l with ʲ, a/ə vowels.
        t["rus"] = {
            {U"\u026D", U"l"},
            {U"\u0283", U"\u0282"},
            {U"\u0292", U"\u0290"},
            {U"y", U"\u0268"},
            {U"\u0251", U"a"},
            {U"\u028C", U"\u0259"},
        };
        return t;
    }();
    return table;
}

std::optional<u32> harmonize_espeak(const u32 & raw, const std::string & code, bool ties) {
    u32 s; // re.sub(r"\([a-z-]+\)", "", raw): (en) voice switches
    for (size_t i = 0; i < raw.size();) {
        if (raw[i] == U'(') {
            size_t j = i + 1;
            while (j < raw.size() && ((raw[j] >= U'a' && raw[j] <= U'z') || raw[j] == U'-')) ++j;
            if (j > i + 1 && j < raw.size() && raw[j] == U')') {
                i = j + 1;
                continue;
            }
        }
        s.push_back(raw[i++]);
    }
    s = nfc(s);
    static const u32 drop = U" \u02C8\u02CC.\u203F'-_"; // _ESPEAK_DROP
    u32 t;
    t.reserve(s.size());
    for (char32_t c : s) {
        if (drop.find(c) != u32::npos) continue;
        t.push_back(c == U':' ? U'\u02D0' : c == U'g' ? U'\u0261'
                                                      : c);
    }
    const auto & table = espeak_symbols();
    if (auto it = table.find(code); it != table.end())
        for (const auto & [a, b] : it->second) replace_all(t, a, b);
    if (ties) {
        u32 tied;
        for (size_t i = 0; i < t.size();) {
            if (affricate_at(t, i)) {
                tied += {t[i], TIE, t[i + 1]};
                i += 2;
            } else tied.push_back(t[i++]);
        }
        t.swap(tied);
    }
    if (t.empty()) return std::nullopt;
    for (char32_t c : t)
        if (c == U'(' || c == U')' || c == U'/' || c == U'[' || c == U']' || (c >= U'0' && c <= U'9')) return std::nullopt;
    return t;
}

// ---------------------------------------------------------------- espeak-ng (dlopen)

class espeak {
  public:
    // The optional guesser (phonemes.py _espeak): null without ZONOS3_ESPEAK_NG_LIBRARY or when it fails to load.
    static espeak * get() {
        static std::unique_ptr<espeak> instance = load();
        return instance.get();
    }
    bool has(const std::string & code) const { return voices_.count(code) != 0; }

    std::string ipa(const std::string & code, const std::string & word) { // raw espeak-ng IPA of one word
        std::lock_guard<std::mutex> lock(mu_);
        const std::string & voice = voices_.at(code);
        if (voice_ != voice && !select(voice)) throw std::runtime_error("espeak-ng voice " + voice + " vanished");
        const void * text = word.c_str();
        std::string out;
        while (text && *(const char *) text) {
            const char * phonemes = text_to_phonemes_(&text, 1, 0x02); // UTF-8 in, IPA out
            if (phonemes) out += phonemes;
        }
        return out;
    }

  private:
    using initialize_fn = int (*)(int, int, const char *, int);
    using set_voice_fn = int (*)(const char *);
    using text_to_phonemes_fn = const char * (*) (const void **, int, int);

    static void * symbol(void * lib, const char * name) {
#ifdef _WIN32
        return (void *) GetProcAddress((HMODULE) lib, name);
#else
        return dlsym(lib, name);
#endif
    }

    static std::unique_ptr<espeak> load() {
        const char * library = std::getenv("ZONOS3_ESPEAK_NG_LIBRARY");
        if (!library || !*library) return nullptr;
        const char * data = std::getenv("ZONOS3_ESPEAK_NG_DATA");
#ifdef _WIN32
        void * lib = (void *) LoadLibraryA(library);
#else
        void * lib = dlopen(library, RTLD_NOW | RTLD_LOCAL);
#endif
        if (!lib) {
            fprintf(stderr, "espeak-ng: cannot load %s; pronunciation guessing is off\n", library);
            return nullptr;
        }
        std::unique_ptr<espeak> e(new espeak());
        auto init = (initialize_fn) symbol(lib, "espeak_Initialize");
        e->set_voice_ = (set_voice_fn) symbol(lib, "espeak_SetVoiceByName");
        e->text_to_phonemes_ = (text_to_phonemes_fn) symbol(lib, "espeak_TextToPhonemes");
        if (!init || !e->set_voice_ || !e->text_to_phonemes_) {
            fprintf(stderr, "espeak-ng: %s lacks the espeak API; pronunciation guessing is off\n", library);
            return nullptr;
        }
        if (init(2, 0, data && *data ? data : nullptr, 0) <= 0) { // AUDIO_OUTPUT_SYNCHRONOUS
            fprintf(stderr, "espeak-ng failed to initialize from %s; pronunciation guessing is off\n", data ? data : "(default)");
            return nullptr;
        }
        for (const auto & [code, voice] : zonos3_espeak_voices())
            if (e->select(voice)) e->voices_[code] = voice;
        return e;
    }

    bool select(const std::string & voice) {
        const bool ok = set_voice_(voice.c_str()) == 0;
        voice_ = ok ? voice : std::string();
        return ok;
    }

    set_voice_fn set_voice_ = nullptr;
    text_to_phonemes_fn text_to_phonemes_ = nullptr;
    std::mutex mu_;
    std::string voice_;
    std::map<std::string, std::string> voices_;
};

// ---------------------------------------------------------------- LMDB

struct read_txn {
    MDB_txn * txn = nullptr;
    int rc = 0;
    explicit read_txn(MDB_env * env) { rc = mdb_txn_begin(env, nullptr, MDB_RDONLY, &txn); }
    ~read_txn() {
        if (txn) mdb_txn_abort(txn);
    }
};

std::optional<std::string> lookup(MDB_txn * txn, MDB_dbi dbi, const u32 & word) {
    const std::string key = utf8_from_u32(normalize_headword(word));
    if (key.empty() || key.size() > MAX_KEY_BYTES) return std::nullopt;
    MDB_val k{key.size(), (void *) key.data()}, v{};
    if (mdb_get(txn, dbi, &k, &v) != 0) return std::nullopt;
    return std::string((const char *) v.mv_data, v.mv_size);
}

// Python repr() of a JSON scalar, for the version error.
std::string py_repr(const nlohmann::ordered_json & v) {
    if (v.is_null()) return "None";
    if (v.is_boolean()) return v.get<bool>() ? "True" : "False";
    if (!v.is_string()) return v.dump();
    const std::string s = v.get<std::string>();
    const char q = s.find('\'') != std::string::npos && s.find('"') == std::string::npos ? '"' : '\'';
    std::string out(1, q);
    for (char c : s) {
        if (c == '\\' || c == q) {
            out += '\\';
            out += c;
        } else if (c == '\n') out += "\\n";
        else if (c == '\r') out += "\\r";
        else if (c == '\t') out += "\\t";
        else if ((unsigned char) c < 0x20 || c == 0x7F) {
            char b[8];
            snprintf(b, sizeof b, "\\x%02x", (unsigned char) c);
            out += b;
        } else out += c;
    }
    return out + q;
}

struct span {
    size_t a, b;
    std::optional<std::string> ipa;
    std::string status;
};

// Runs that start at a letter and continue over letters, marks and inner apostrophes.
std::vector<std::pair<size_t, size_t>> word_spans(const u32 & text) {
    std::vector<std::pair<size_t, size_t>> spans;
    const size_t n = text.size();
    for (size_t i = 0; i < n;) {
        if (!char_class(text[i])) {
            ++i;
            continue;
        }
        size_t j = i + 1;
        while (j < n) {
            if (is_letter(text[j]) || is_mark(text[j])) ++j;
            else if ((text[j] == U'\'' || text[j] == U'\u2019') && j + 1 < n && char_class(text[j + 1])) j += 2;
            else break;
        }
        spans.push_back({i, j});
        i = j;
    }
    return spans;
}

// Forward maximum matching of 2+ character headwords inside one run; uncovered characters are "skipped".
void max_match(MDB_txn * txn, MDB_dbi dbi, const u32 & text, size_t start, size_t end, std::vector<span> & spans) {
    size_t i = start;
    std::optional<size_t> pending;
    while (i < end) {
        bool hit = false;
        for (size_t length = std::min(CJK_MAX, end - i); length > 1; --length) {
            std::optional<std::string> value = lookup(txn, dbi, text.substr(i, length));
            if (value) {
                if (pending) {
                    spans.push_back({*pending, i, std::nullopt, "skipped"});
                    pending.reset();
                }
                const char * st = status_of(value);
                spans.push_back({i, i + length, value->empty() ? std::nullopt : value, st});
                i += length;
                hit = true;
                break;
            }
        }
        if (!hit) {
            if (!pending) pending = i;
            ++i;
        }
    }
    if (pending) spans.push_back({*pending, end, std::nullopt, "skipped"});
}

// Korean eojeol; Japanese/Chinese headwords of 2+ characters (phonemes.py _cjk_spans).
std::vector<span> cjk_spans(MDB_txn * txn, MDB_dbi dbi, const u32 & text, const std::string & mode) {
    static const u32 ja_particles = U"\u306F\u304C\u3092\u306B\u3067\u3068\u306E\u3082\u3078"; // はがをにでとのもへ
    std::set<int> targets;
    if (mode == "korean") targets = {HANGUL};
    else if (mode == "japanese") targets = {HAN, KATA};
    else targets = {HAN};
    std::vector<span> spans;
    const size_t n = text.size();
    for (size_t i = 0; i < n;) {
        const int cls = char_class(text[i]);
        if (!targets.count(cls)) {
            ++i;
            continue;
        }
        size_t j = i + 1;
        while (j < n && char_class(text[j]) == cls) ++j;
        if (mode == "korean") {
            std::optional<std::string> value = lookup(txn, dbi, text.substr(i, j - i));
            const char * st = status_of(value);
            spans.push_back({i, j, value && !value->empty() ? value : std::nullopt, st});
        } else if (cls == HAN && mode == "japanese" && j < n && char_class(text[j]) == HIRA &&
                   ja_particles.find(text[j]) == u32::npos) {
            spans.push_back({i, j, std::nullopt, "skipped"}); // kanji + okurigana: the inflected word's reading
        } else {
            max_match(txn, dbi, text, i, j, spans);
        }
        i = j;
    }
    return spans;
}

} // namespace

std::string zonos3_normalize_headword(const std::string & word) {
    return utf8_from_u32(normalize_headword(utf8_to_u32(word)));
}

std::optional<std::string> zonos3_harmonize_espeak(const std::string & raw, const std::string & code, bool ties) {
    std::optional<u32> s = harmonize_espeak(utf8_to_u32(raw), code, ties);
    if (!s) return std::nullopt;
    return utf8_from_u32(*s);
}

bool zonos3_espeak_available() {
    return espeak::get() != nullptr;
}

std::unique_ptr<zonos3_phoneme_lexicon> zonos3_phoneme_lexicon::open(const std::string & path, std::string & err) {
    std::unique_ptr<zonos3_phoneme_lexicon> lex(new zonos3_phoneme_lexicon());
    int rc = mdb_env_create(&lex->env_);
    if (rc == 0) rc = mdb_env_set_maxdbs(lex->env_, 64);
    if (rc == 0) rc = mdb_env_open(lex->env_, path.c_str(), MDB_RDONLY | MDB_NOLOCK | MDB_NORDAHEAD, 0644);
    if (rc != 0) {
        err = path + ": " + mdb_strerror(rc);
        return nullptr;
    }
    read_txn t(lex->env_);
    if (t.rc != 0) {
        err = path + ": " + mdb_strerror(t.rc);
        return nullptr;
    }
    MDB_dbi main_db = 0;
    if ((rc = mdb_dbi_open(t.txn, nullptr, 0, &main_db)) != 0) {
        err = path + ": " + mdb_strerror(rc);
        return nullptr;
    }
    MDB_val k{4, (void *) "meta"}, v{};
    if (mdb_get(t.txn, main_db, &k, &v) != 0) {
        err = path + " is not a phoneme lexicon (no meta record)";
        return nullptr;
    }
    nlohmann::ordered_json meta;
    try {
        meta = nlohmann::ordered_json::parse(std::string((const char *) v.mv_data, v.mv_size));
    } catch (const std::exception & e) {
        err = path + ": bad meta record: " + e.what();
        return nullptr;
    }
    const nlohmann::ordered_json version = meta.is_object() && meta.contains("version") ? meta["version"] : nullptr;
    if (!(version.is_number() || version.is_boolean()) || version.get<double>() != LEXICON_VERSION) {
        err = "Unsupported phoneme lexicon version " + py_repr(version);
        return nullptr;
    }
    if (!meta.contains("languages") || !meta["languages"].is_object()) {
        err = path + ": meta has no languages";
        return nullptr;
    }
    for (const auto & [code, info] : meta["languages"].items()) {
        MDB_dbi dbi = 0;
        if ((rc = mdb_dbi_open(t.txn, code.c_str(), 0, &dbi)) != 0) {
            err = path + ": " + code + ": " + mdb_strerror(rc);
            return nullptr;
        }
        lex->codes_.push_back(code);
        lex->dbs_[code] = dbi;
        lex->modes_[code] = info.is_object() && info.contains("mode") && info["mode"].is_string() ? info["mode"].get<std::string>() : "";
    }
    rc = mdb_txn_commit(t.txn); // keeps the opened DB handles
    t.txn = nullptr;
    if (rc != 0) {
        err = path + ": " + mdb_strerror(rc);
        return nullptr;
    }
    return lex;
}

zonos3_phoneme_lexicon::~zonos3_phoneme_lexicon() {
    if (env_) mdb_env_close(env_);
}

bool zonos3_phoneme_lexicon::resolve_language(const std::string & language, std::string & code, std::string & err) const {
    code = language;
    for (const auto & l : zonos3_server_languages())
        if (l.code == language) {
            code = l.lexicon;
            break;
        }
    if (!dbs_.count(code)) {
        err = "No pronunciation lexicon for language " + py_repr(language);
        return false;
    }
    return true;
}

std::vector<std::string> zonos3_phoneme_lexicon::server_languages() const {
    std::vector<std::string> out;
    for (const auto & l : zonos3_server_languages())
        if (dbs_.count(l.lexicon)) out.push_back(l.code);
    return out;
}

std::vector<std::string> zonos3_phoneme_lexicon::guess_languages() const {
    std::vector<std::string> out;
    espeak * e = espeak::get();
    if (!e) return out;
    for (const auto & l : zonos3_server_languages())
        if (dbs_.count(l.lexicon) && e->has(l.lexicon)) out.push_back(l.code);
    return out;
}

std::optional<std::string> zonos3_phoneme_lexicon::get(const std::string & code, const std::string & word) const {
    read_txn t(env_);
    if (t.rc != 0) return std::nullopt;
    return lookup(t.txn, (MDB_dbi) dbs_.at(code), utf8_to_u32(word));
}

bool zonos3_phoneme_lexicon::uses_ties(const std::string & code) {
    std::lock_guard<std::mutex> lock(ties_mu_);
    if (auto it = ties_.find(code); it != ties_.end()) return it->second;
    size_t tied = 0, untied = 0;
    read_txn t(env_);
    MDB_cursor * cur = nullptr;
    if (t.rc == 0 && mdb_cursor_open(t.txn, (MDB_dbi) dbs_.at(code), &cur) == 0) {
        MDB_val k{}, v{};
        size_t seen = 0;
        for (int rc = mdb_cursor_get(cur, &k, &v, MDB_FIRST); rc == 0 && seen < TIES_SAMPLE;
             rc = mdb_cursor_get(cur, &k, &v, MDB_NEXT), ++seen) {
            const u32 value = utf8_to_u32(std::string_view((const char *) v.mv_data, v.mv_size));
            tied += (size_t) std::count(value.begin(), value.end(), TIE);
            untied += count_affricates(value);
        }
        mdb_cursor_close(cur);
    }
    return ties_[code] = tied > untied;
}

bool zonos3_phoneme_lexicon::phonemize(const std::string & text_utf8, const std::string & language, bool guess,
                                       zonos3_phonemize_result & out, std::string & err) {
    std::string code;
    if (!resolve_language(language, code, err)) return false;
    const std::string & mode = modes_.at(code);
    espeak * e = guess ? espeak::get() : nullptr;
    if (e && !e->has(code)) e = nullptr;
    const bool ties = e && mode == "words" && uses_ties(code); // before our read txn: one per thread
    const u32 text = utf8_to_u32(text_utf8);
    read_txn t(env_);
    if (t.rc != 0) {
        err = std::string("lexicon read failed: ") + mdb_strerror(t.rc);
        return false;
    }
    const MDB_dbi dbi = (MDB_dbi) dbs_.at(code);
    std::vector<span> spans;
    if (mode == "words") {
        for (const auto & [a, b] : word_spans(text)) {
            const u32 word = text.substr(a, b - a);
            std::optional<std::string> value = lookup(t.txn, dbi, word);
            std::optional<u32> guessed;
            if ((!value || value->empty()) && e)
                guessed = harmonize_espeak(utf8_to_u32(e->ipa(code, utf8_from_u32(word))), code, ties);
            if (guessed) spans.push_back({a, b, utf8_from_u32(*guessed), "guessed"});
            else {
                const char * st = status_of(value);
                spans.push_back({a, b, value && !value->empty() ? value : std::nullopt, st});
            }
        }
    } else {
        spans = cjk_spans(t.txn, dbi, text, mode);
    }
    out.language = code;
    out.segments.clear();
    size_t pos = 0;
    for (const span & s : spans) {
        if (s.a > pos) out.segments.push_back({utf8_from_u32(std::u32string_view(text).substr(pos, s.a - pos)), std::nullopt, std::nullopt});
        out.segments.push_back({utf8_from_u32(std::u32string_view(text).substr(s.a, s.b - s.a)), s.ipa, s.status});
        pos = s.b;
    }
    if (pos < text.size()) out.segments.push_back({utf8_from_u32(std::u32string_view(text).substr(pos)), std::nullopt, std::nullopt});
    return true;
}

void zonos3_phonemize_restrict(zonos3_phonemize_result & result, const std::vector<std::string> & symbols) {
    // api_server.py tts_phonemize: symbols.issuperset(ipa) tests every code point of ipa as a one-character symbol.
    std::set<char32_t> single;
    for (const std::string & s : symbols) {
        const u32 cps = utf8_to_u32(s);
        if (cps.size() == 1) single.insert(cps[0]);
    }
    for (zonos3_phoneme_segment & seg : result.segments) {
        if (!seg.ipa || seg.ipa->empty()) continue;
        const u32 ipa = utf8_to_u32(*seg.ipa);
        if (std::all_of(ipa.begin(), ipa.end(), [&](char32_t c) { return single.count(c) != 0; })) continue;
        seg.ipa.reset();
        seg.status = "unsupported";
    }
}

std::string zonos3_phonemize_json(const zonos3_phonemize_result & result) {
    nlohmann::ordered_json segments = nlohmann::ordered_json::array();
    for (const zonos3_phoneme_segment & s : result.segments) {
        nlohmann::ordered_json seg;
        seg["text"] = s.text;
        seg["ipa"] = s.ipa ? nlohmann::ordered_json(*s.ipa) : nlohmann::ordered_json(nullptr);
        seg["status"] = s.status ? nlohmann::ordered_json(*s.status) : nlohmann::ordered_json(nullptr);
        segments.push_back(std::move(seg));
    }
    nlohmann::ordered_json out;
    out["language"] = result.language;
    out["segments"] = std::move(segments);
    return out.dump();
}

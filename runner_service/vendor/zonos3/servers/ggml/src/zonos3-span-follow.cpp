// Exact port of model/src/zonos3/tts/span_follow.py (see zonos3-span-follow.h).
#include "zonos3-span-follow.h"
#include "zonos3-text.h"

#include <algorithm>
#include <array>
#include <cmath>
#include <stdexcept>
#include <tuple>
#include <unordered_map>

namespace zonos3_follow {

namespace {

template <typename T>
double median(std::vector<T> v) { // statistics.median
    std::sort(v.begin(), v.end());
    const size_t n = v.size(), i = n / 2;
    if (n % 2 == 1) return (double) v[i];
    return ((double) v[i - 1] + (double) v[i]) / 2;
}

struct Match {
    int i, j, k;
};

// difflib.SequenceMatcher with isjunk=None, autojunk=False: no junk and no popular elements.
struct Matcher {
    const std::u32string & a;
    const std::u32string & b;
    std::unordered_map<char32_t, std::vector<int>> b2j;

    Matcher(const std::u32string & a_, const std::u32string & b_) : a(a_), b(b_) {
        for (int i = 0; i < (int) b.size(); i++) b2j[b[i]].push_back(i);
    }

    Match find_longest_match(int alo, int ahi, int blo, int bhi) const {
        int besti = alo, bestj = blo, bestsize = 0;
        std::unordered_map<int, int> j2len, newj2len;
        static const std::vector<int> nothing;
        for (int i = alo; i < ahi; i++) {
            newj2len.clear();
            const auto it = b2j.find(a[i]);
            const auto & js = it == b2j.end() ? nothing : it->second;
            for (int j : js) {
                if (j < blo) continue;
                if (j >= bhi) break;
                const auto prev = j2len.find(j - 1);
                const int k = newj2len[j] = (prev == j2len.end() ? 0 : prev->second) + 1;
                if (k > bestsize) {
                    besti = i - k + 1;
                    bestj = j - k + 1;
                    bestsize = k;
                }
            }
            std::swap(j2len, newj2len);
        }
        while (besti > alo && bestj > blo && a[besti - 1] == b[bestj - 1]) {
            besti--;
            bestj--;
            bestsize++;
        }
        while (besti + bestsize < ahi && bestj + bestsize < bhi && a[besti + bestsize] == b[bestj + bestsize])
            bestsize++;
        return {besti, bestj, bestsize};
    }

    std::vector<Match> matching_blocks() const {
        const int la = (int) a.size(), lb = (int) b.size();
        std::vector<std::array<int, 4>> queue = {{0, la, 0, lb}};
        std::vector<Match> blocks;
        while (!queue.empty()) {
            const auto [alo, ahi, blo, bhi] = queue.back();
            queue.pop_back();
            const Match x = find_longest_match(alo, ahi, blo, bhi);
            if (x.k) {
                blocks.push_back(x);
                if (alo < x.i && blo < x.j) queue.push_back({alo, x.i, blo, x.j});
                if (x.i + x.k < ahi && x.j + x.k < bhi) queue.push_back({x.i + x.k, ahi, x.j + x.k, bhi});
            }
        }
        std::sort(blocks.begin(), blocks.end(), [](const Match & p, const Match & q) {
            return std::tie(p.i, p.j, p.k) < std::tie(q.i, q.j, q.k);
        });
        int i1 = 0, j1 = 0, k1 = 0;
        std::vector<Match> out;
        for (const Match & m : blocks) {
            if (i1 + k1 == m.i && j1 + k1 == m.j) {
                k1 += m.k;
            } else {
                if (k1) out.push_back({i1, j1, k1});
                i1 = m.i;
                j1 = m.j;
                k1 = m.k;
            }
        }
        if (k1) out.push_back({i1, j1, k1});
        out.push_back({la, lb, 0});
        return out;
    }
};

} // namespace

bool py_isspace(char32_t c) {
    return (c >= 0x09 && c <= 0x0D) || (c >= 0x1C && c <= 0x20) || c == 0x85 || c == 0xA0 || c == 0x1680 ||
           (c >= 0x2000 && c <= 0x200A) || c == 0x2028 || c == 0x2029 || c == 0x202F || c == 0x205F || c == 0x3000;
}

std::vector<Opcode> diff_opcodes(const std::u32string & a, const std::u32string & b) {
    const Matcher sm(a, b);
    int i = 0, j = 0;
    std::vector<Opcode> answer;
    for (const Match & m : sm.matching_blocks()) {
        const char * tag = nullptr;
        if (i < m.i && j < m.j) tag = "replace";
        else if (i < m.i) tag = "delete";
        else if (j < m.j) tag = "insert";
        if (tag) answer.push_back({tag, i, m.i, j, m.j});
        i = m.i + m.k;
        j = m.j + m.k;
        if (m.k) answer.push_back({"equal", m.i, i, m.j, j});
    }
    return answer;
}

std::vector<int> map_offsets(const std::u32string & original, const std::u32string & final_,
                             const std::vector<int> & offsets, bool end) {
    if (original == final_) return offsets;
    // the blocks holding original characters partition [0, len(original)); insertions hold none
    std::vector<Opcode> blocks;
    for (const Opcode & op : diff_opcodes(original, final_))
        if (op.i2 > op.i1) blocks.push_back(op);
    std::vector<int> out;
    out.reserve(offsets.size());
    for (int o : offsets) {
        if (o >= (int) original.size()) {
            out.push_back((int) final_.size());
            continue;
        }
        // blocks[bisect_right(starts, o) - 1]
        const auto it = std::upper_bound(blocks.begin(), blocks.end(), o,
                                         [](int v, const Opcode & op) { return v < op.i1; });
        const Opcode & op = *(it - 1);
        if (std::string(op.tag) == "equal") {
            out.push_back(op.j1 + o - op.i1);
        } else {
            const double x = op.j1 + (double) (o - op.i1) * (op.j2 - op.j1) / (op.i2 - op.i1);
            out.push_back(end ? (int) std::ceil(x) : (int) x);
        }
    }
    return out;
}

std::vector<Range> token_char_ranges(const std::u32string & text, const BpeOffsets & bpe, const TextMarkers & markers) {
    const int n = (int) text.size();
    std::vector<Range> ranges = {{0, 0}};
    if (!bpe) {
        for (int i = 0; i < n; i++)
            for (int b = 0; b < (int) utf8_encoded_length(text[i]); b++) ranges.push_back({i, i + 1});
    } else {
        auto stretch = [&](const std::u32string & s, int base) {
            if (s.empty()) return;
            for (const auto & [a, b] : bpe(s)) ranges.push_back({base + a, base + b});
        };
        auto is_pause = [](char32_t c) { return c >= 0xE003 && c <= 0xE00A; };
        // superbpe.payload_ids validated the text: the start marker opens it, groups are balanced.
        std::function<void(const std::u32string &, int)> payload = [&](const std::u32string & s, int base) {
            if (markers.start && s.find(U'\uE002') != std::u32string::npos) {
                ranges.push_back({base, base + 1});
                payload(s.substr(1), base + 1);
                return;
            }
            if (markers.pauses && std::any_of(s.begin(), s.end(), is_pause)) {
                size_t pos = 0;
                for (size_t i = 0; i < s.size(); i++) {
                    if (!is_pause(s[i])) continue;
                    if (i > pos) payload(s.substr(pos, i - pos), base + (int) pos);
                    ranges.push_back({base + (int) i, base + (int) i + 1});
                    pos = i + 1;
                }
                if (pos < s.size()) payload(s.substr(pos), base + (int) pos);
                return;
            }
            const auto & ph = markers.phonemes;
            if (!ph || (s.find(ph->open) == std::u32string::npos && s.find(ph->close) == std::u32string::npos)) {
                stretch(s, base);
                return;
            }
            size_t pos = 0;
            while (pos < s.size()) {
                const size_t start = s.find(ph->open, pos);
                stretch(start == std::u32string::npos ? s.substr(pos) : s.substr(pos, start - pos), base + (int) pos);
                if (start == std::u32string::npos) break;
                const size_t end = s.find(ph->close, start + 1);
                for (size_t i = start; i <= end; i++) ranges.push_back({base + (int) i, base + (int) i + 1});
                pos = end + 1;
            }
        };
        payload(text, 0);
    }
    ranges.push_back({n, n});
    return ranges;
}

std::vector<Range> span_token_ranges(const std::u32string & text, const std::u32string & final_,
                                     const std::vector<Range> & spans, const std::vector<Range> & ranges) {
    std::vector<Range> trimmed;
    trimmed.reserve(ranges.size());
    for (auto [a, b] : ranges) {
        while (a < b && py_isspace(final_[a])) a++;
        while (b > a && py_isspace(final_[b - 1])) b--;
        trimmed.push_back({a, b});
    }
    std::vector<Range> out;
    for (auto [start, end] : spans) {
        while (start < end && py_isspace(text[start])) start++;
        while (end > start && py_isspace(text[end - 1])) end--;
        const int s = map_offsets(text, final_, {start})[0], e = map_offsets(text, final_, {end}, true)[0];
        std::vector<int> hit;
        for (int i = 0; i < (int) trimmed.size(); i++) {
            const auto [a, b] = trimmed[i];
            if (a < b && a < e && s < b) {
                hit.push_back(i);
            }
        }
        if (!hit.empty()) {
            out.push_back({hit.front(), hit.back() + 1});
        } else {
            int first = (int) ranges.size() - 1;
            for (int i = 0; i < (int) trimmed.size(); i++)
                if (trimmed[i].first < trimmed[i].second && trimmed[i].second > s) {
                    first = i;
                    break;
                }
            out.push_back({first, first});
        }
    }
    return out;
}

// ---------------------------------------------------------------------------------------------------- SpanFollower

SpanFollower::SpanFollower(std::vector<Range> ranges_, std::vector<std::vector<float>> deltas_, FollowConfig cfg_,
                           int origin_)
    : ranges(std::move(ranges_)), deltas(std::move(deltas_)), cfg(cfg_), origin(origin_) {
    bool flat = true;
    for (const auto & d : deltas) flat = flat && d.size() == deltas.front().size();
    if (!flat || deltas.size() != ranges.size()) {
        std::string shape = "(" + std::to_string(deltas.size()) + ", " +
                            (deltas.empty() ? std::string("0") : std::to_string(deltas.front().size())) + ")";
        throw std::invalid_argument("span follower: " + std::to_string(ranges.size()) +
                                    " token ranges for deltas of shape " + shape);
    }
    if (cfg.smooth < 1 || cfg.ramp < 1) throw std::invalid_argument("span follower: smooth and ramp must be >= 1");
    enter_.assign(ranges.size(), std::nullopt);
    exit_.assign(ranges.size(), std::nullopt);
}

void SpanFollower::observe(int frame, int pos) {
    if (!frames_.empty() && frame <= frames_.back())
        throw std::invalid_argument("span follower: frames must increase, got " + std::to_string(frame) + " after " +
                                    std::to_string(frames_.back()));
    frames_.push_back(frame);
    pos_.push_back(pos);
    advance(pos_.size());
}

void SpanFollower::advance(size_t n) {
    const size_t lo = n > (size_t) cfg.smooth ? n - cfg.smooth : 0;
    const double med = median(std::vector<int>(pos_.begin() + lo, pos_.begin() + n));
    best_ = std::min(std::max(best_, med), best_ + 1.0);
}

const std::vector<double> & SpanFollower::weights(int frame) {
    const auto found = weights_.find(frame);
    if (found != weights_.end()) return found->second;
    const double s = best_;
    for (size_t k = 0; k < ranges.size(); k++) {
        const auto [a, b] = ranges[k];
        if (a >= b) continue;
        if (!enter_[k] && s >= a - cfg.lead)
            // on before any readout (a span at the start of the text): full weight at once, like the schedule
            enter_[k] = std::make_pair(frame, frames_.empty() ? frame - cfg.ramp + 1 : frame);
        if (enter_[k] && !exit_[k] && s >= b + cfg.lag) exit_[k] = std::make_pair(frame, frame);
    }
    std::vector<double> w;
    w.reserve(ranges.size());
    for (size_t k = 0; k < ranges.size(); k++) {
        if (!enter_[k]) {
            w.push_back(0.0);
            continue;
        }
        const double up = (double) (frame - enter_[k]->second + 1) / cfg.ramp;
        const double down = !exit_[k] ? 1.0 : (double) (exit_[k]->second + cfg.ramp - 1 - frame) / cfg.ramp;
        w.push_back(std::min(1.0, std::max(0.0, std::min(up, down))));
    }
    return weights_.emplace(frame, std::move(w)).first->second;
}

bool SpanFollower::delta(int frame, std::vector<float> & out) {
    const std::vector<double> & w = weights(frame);
    if (std::none_of(w.begin(), w.end(), [](double x) { return x > 0; })) return false;
    const size_t dim = deltas.empty() ? 0 : deltas.front().size();
    out.assign(dim, 0.0f);
    for (size_t k = 0; k < w.size(); k++) {
        const float wk = (float) w[k];
        for (size_t d = 0; d < dim; d++) out[d] += wk * deltas[k][d];
    }
    return true;
}

void SpanFollower::truncate(int frame) {
    const size_t keep = std::lower_bound(frames_.begin(), frames_.end(), frame) - frames_.begin();
    frames_.resize(keep);
    pos_.resize(keep);
    best_ = 0.0;
    for (size_t n = 1; n <= keep; n++) advance(n);
    for (auto * events : {&enter_, &exit_})
        for (auto & e : *events)
            if (e && e->first >= frame) e.reset();
    weights_.erase(weights_.lower_bound(frame), weights_.end());
}

} // namespace zonos3_follow

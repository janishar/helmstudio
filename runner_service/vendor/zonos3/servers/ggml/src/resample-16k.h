// Torchaudio Resample(44100, 16000) polyphase kernel applied on the host.
// Shared by the SeamlessM4T/W2v-BERT teacher frontend and the speaker encoder.
#pragma once
#include <algorithm>
#include <array>
#include <cmath>
#include <cstdint>
#include <utility>
#include <vector>

struct resample_16k {
    static constexpr int orig = 441, rate = 160, width = 17, kernel_size = 475;
    // Torchaudio's default F64-generated, F32-stored sinc/Hann polyphase kernel, [160][475].
    std::vector<float> kernel;
    // Non-zero support [begin, end) of each phase's kernel.
    std::array<int, 160> begin{}, end{};

    void init(std::vector<float> k) {
        kernel = std::move(k);
        for (int phase = 0; phase < rate; ++phase) {
            const float * row = kernel.data() + (size_t) phase * kernel_size;
            int b = 0, e = kernel_size;
            while (b < e && row[b] == 0.0f) ++b;
            while (e > b && row[e - 1] == 0.0f) --e;
            begin[phase] = b; end[phase] = e;
        }
    }
    void clear() { kernel.clear(); }

    // 44.1 kHz mono PCM -> 16 kHz mono PCM.
    std::vector<float> run(const float * x, int64_t n) const {
        const int count = (int) std::ceil((float) ((double) rate * n / orig));
        std::vector<float> wav(count);
        for (int i = 0; i < count; ++i) {
            const int phase = i % rate;
            const int64_t start = (int64_t) (i / rate) * orig - width;
            const float * row = kernel.data() + (size_t) phase * kernel_size;
            float sum = 0.0f;
            const int lo = (int) std::max<int64_t>(begin[phase], -start);
            const int hi = (int) std::min<int64_t>(end[phase], n - start);
            for (int j = lo; j < hi; ++j) sum += x[start + j] * row[j];
            wav[i] = sum;
        }
        return wav;
    }
};

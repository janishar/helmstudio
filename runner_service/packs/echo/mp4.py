"""A tiny valid MP4 in pure Python, for the fake runner (no ffmpeg in its image): H.264 Constrained Baseline
(frame 0 an IDR of I_PCM macroblocks, i.e. the image's samples uncompressed; every later frame a P frame of skipped
macroblocks, so the clip holds still) and AAC-LC 48 kHz stereo silence, moov first (faststart) so it streams with
Range requests. Sides must be multiples of 16."""

import hashlib
import math
import struct

from PIL import Image

RATE = 48000  # audio samples per second
AAC_FRAME = 1024  # samples per AAC frame


class Bits:
    """An MSB-first bit writer with Exp-Golomb codes."""

    def __init__(self):
        self.buf, self.acc, self.n = bytearray(), 0, 0

    def u(self, value: int, n: int) -> None:
        for i in reversed(range(n)):
            self.acc, self.n = (self.acc << 1) | ((value >> i) & 1), self.n + 1
            if self.n == 8:
                self.buf.append(self.acc)
                self.acc, self.n = 0, 0

    def ue(self, value: int) -> None:
        n = (value + 1).bit_length()
        self.u(0, n - 1)
        self.u(value + 1, n)

    def align(self) -> None:
        while self.n:
            self.u(0, 1)

    def raw(self, data: bytes) -> None:
        self.buf += data  # byte-aligned only

    def rbsp(self) -> bytes:
        self.u(1, 1)  # rbsp_stop_one_bit
        self.align()
        return bytes(self.buf)


def nal(header: int, rbsp: bytes) -> bytes:
    """A NAL unit: its header byte and the payload with emulation-prevention bytes."""
    out, zeros = bytearray([header]), 0
    for b in rbsp:
        if zeros >= 2 and b <= 3:
            out.append(3)
            zeros = 0
        out.append(b)
        zeros = zeros + 1 if b == 0 else 0
    return bytes(out)


def params(w: int, h: int) -> tuple[bytes, bytes]:
    """(SPS, PPS) of a w × h clip."""
    mbw, mbh = w // 16, h // 16
    s = Bits()
    s.u(66, 8)  # profile_idc: Baseline
    s.u(0xC0, 8)  # constraint_set0/1: Constrained Baseline
    s.u(40, 8)  # level 4.0: up to 8192 macroblocks a frame
    s.ue(0)  # seq_parameter_set_id
    s.ue(12)  # log2_max_frame_num_minus4: 16-bit frame_num
    s.ue(2)  # pic_order_cnt_type 2: output order = decode order
    s.ue(1)  # max_num_ref_frames
    s.u(0, 1)  # gaps_in_frame_num_value_allowed_flag
    s.ue(mbw - 1)
    s.ue(mbh - 1)
    s.u(1, 1)  # frame_mbs_only_flag
    s.u(1, 1)  # direct_8x8_inference_flag
    s.u(0, 1)  # frame_cropping_flag
    s.u(0, 1)  # vui_parameters_present_flag
    sps = nal(0x67, s.rbsp())

    p = Bits()
    p.ue(0)  # pic_parameter_set_id
    p.ue(0)  # seq_parameter_set_id
    p.u(0, 1)  # entropy_coding_mode_flag: CAVLC
    p.u(0, 1)  # bottom_field_pic_order_in_frame_present_flag
    p.ue(0)  # num_slice_groups_minus1
    p.ue(0)  # num_ref_idx_l0_default_active_minus1
    p.ue(0)  # num_ref_idx_l1_default_active_minus1
    p.u(0, 1)  # weighted_pred_flag
    p.u(0, 2)  # weighted_bipred_idc
    p.ue(0)  # pic_init_qp_minus26 (se 0 = ue 0)
    p.ue(0)  # pic_init_qs_minus26
    p.ue(0)  # chroma_qp_index_offset
    p.u(1, 1)  # deblocking_filter_control_present_flag
    p.u(0, 1)  # constrained_intra_pred_flag
    p.u(0, 1)  # redundant_pic_cnt_present_flag
    pps = nal(0x68, p.rbsp())
    return sps, pps


def h264(image: Image.Image, frames: int) -> tuple[bytes, bytes, list[bytes]]:
    """(SPS, PPS, one access unit per frame as NAL units without start codes)."""
    w, h = image.size
    mbw, mbh = w // 16, h // 16
    sps, pps = params(w, h)
    # limited-range BT.601 4:2:0 samples (never 0, which old decoders refuse in PCM)
    ycc = image.convert("RGB").convert("YCbCr")
    y_, cb, cr = ycc.split()
    y = y_.point(lambda v: 16 + v * 219 // 255).tobytes()
    cb, cr = (
        c.resize((w // 2, h // 2), Image.Resampling.BOX)
        .point(lambda v: 16 + v * 224 // 255)
        .tobytes()
        for c in (cb, cr)
    )
    i = Bits()
    i.ue(0)  # first_mb_in_slice
    i.ue(7)  # slice_type: I (every slice)
    i.ue(0)  # pic_parameter_set_id
    i.u(0, 16)  # frame_num
    i.ue(0)  # idr_pic_id
    i.u(0, 1)  # no_output_of_prior_pics_flag
    i.u(0, 1)  # long_term_reference_flag
    i.ue(0)  # slice_qp_delta
    i.ue(1)  # disable_deblocking_filter_idc: off
    for my in range(mbh):
        for mx in range(mbw):
            i.ue(25)  # mb_type I_PCM
            i.align()  # pcm_alignment_zero_bit
            for r in range(16):
                o = (my * 16 + r) * w + mx * 16
                i.raw(y[o : o + 16])
            for plane in (cb, cr):
                for r in range(8):
                    o = (my * 8 + r) * (w // 2) + mx * 8
                    i.raw(plane[o : o + 8])
    units = [nal(0x65, i.rbsp())]
    for n in range(1, frames):
        b = Bits()
        b.ue(0)  # first_mb_in_slice
        b.ue(5)  # slice_type: P (every slice)
        b.ue(0)  # pic_parameter_set_id
        b.u(n, 16)  # frame_num
        b.u(0, 1)  # num_ref_idx_active_override_flag
        b.u(0, 1)  # ref_pic_list_modification_flag_l0
        b.u(0, 1)  # adaptive_ref_pic_marking_mode_flag
        b.ue(0)  # slice_qp_delta
        b.ue(1)  # disable_deblocking_filter_idc
        b.ue(mbw * mbh)  # mb_skip_run: every macroblock
        units.append(nal(0x41, b.rbsp()))
    return sps, pps, units


def aac_silence() -> bytes:
    """One raw AAC-LC frame of stereo silence: a channel pair with no scale factor bands."""
    b = Bits()
    b.u(1, 3)  # ID_CPE
    b.u(0, 4)  # element_instance_tag
    b.u(0, 1)  # common_window
    for _ in range(2):
        b.u(100, 8)  # global_gain
        b.u(0, 1)  # ics_reserved_bit
        b.u(0, 2)  # window_sequence: ONLY_LONG
        b.u(0, 1)  # window_shape
        b.u(0, 6)  # max_sfb: no bands
        b.u(0, 1)  # predictor_data_present
        b.u(0, 3)  # pulse, tns, gain control: absent
    b.u(7, 3)  # ID_END
    b.align()
    return bytes(b.buf)


def box(kind: bytes, *parts: bytes) -> bytes:
    body = b"".join(parts)
    return struct.pack(">I4s", 8 + len(body), kind) + body


def full(kind: bytes, version_flags: int, *parts: bytes) -> bytes:
    return box(kind, struct.pack(">I", version_flags), *parts)


MATRIX = struct.pack(">9I", 0x10000, 0, 0, 0, 0x10000, 0, 0, 0, 0x40000000)


def descriptor(tag: int, body: bytes) -> bytes:
    return bytes([tag, len(body)]) + body  # every body here is under 128 bytes


def trak(
    tid, movie_ms, timescale, duration, handler, media_header, entry, deltas, sizes, offset, sync
):
    video = handler == b"vide"
    w, h = struct.unpack(">HH", entry[32:36]) if video else (0, 0)
    tkhd = full(
        b"tkhd", 3,  # enabled, in movie
        struct.pack(">IIIII", 0, 0, tid, 0, movie_ms), bytes(8),
        struct.pack(">hhhH", 0, 0, 0 if video else 0x0100, 0), MATRIX,
        struct.pack(">II", w << 16, h << 16),
    )  # fmt: skip
    mdhd = full(b"mdhd", 0, struct.pack(">IIIIHH", 0, 0, timescale, duration, 0x55C4, 0))
    name = b"VideoHandler\0" if video else b"SoundHandler\0"
    hdlr = full(b"hdlr", 0, struct.pack(">I4s", 0, handler), bytes(12), name)
    dinf = box(b"dinf", full(b"dref", 0, struct.pack(">I", 1), full(b"url ", 1)))
    stbl = [
        full(b"stsd", 0, struct.pack(">I", 1), entry),
        full(b"stts", 0, struct.pack(">III", 1, len(sizes), deltas)),
        full(b"stsc", 0, struct.pack(">IIII", 1, 1, len(sizes), 1)),  # one chunk per track
        full(b"stsz", 0, struct.pack(">II", 0, len(sizes)), struct.pack(f">{len(sizes)}I", *sizes)),
        full(b"stco", 0, struct.pack(">II", 1, offset)),
    ]
    if sync:
        stbl.insert(2, full(b"stss", 0, struct.pack(">II", 1, 1)))  # frame 1 is the only key frame
    minf = box(b"minf", media_header, dinf, box(b"stbl", *stbl))
    return box(b"trak", tkhd, box(b"mdia", mdhd, hdlr, minf))


def avcc_payload(sps: bytes, pps: bytes) -> bytes:
    """The avcC box's body: the codec header data (ffprobe's extradata)."""
    return (bytes([1, 66, 0xC0, 40, 0xFF, 0xE1]) + struct.pack(">H", len(sps)) + sps + bytes([1])
            + struct.pack(">H", len(pps)) + pps)  # fmt: skip


def stream(w: int, h: int, fps: int) -> dict:
    """The defs videoStream facts of what mp4() writes at w × h and fps (what ffprobe reads from it)."""
    return {"container": "mp4", "codec": "h264", "profile": "Constrained Baseline", "level": 40,
            "pix_fmt": "yuv420p", "width": w, "height": h, "sar": None, "field_order": "progressive",
            "frame_rate": f"{fps}/1", "time_base": f"1/{fps * 1000}",
            "color": {"range": None, "space": None, "primaries": None, "transfer": None},
            "extradata_sha256": hashlib.sha256(avcc_payload(*params(w, h))).hexdigest()}  # fmt: skip


def mp4(image: Image.Image, frames: int, fps: int) -> bytes:
    """`frames` frames of `image` at `fps`, with as much silent stereo audio, as an MP4 (moov before mdat)."""
    w, h = image.size
    if w % 16 or h % 16 or frames < 1:
        raise ValueError(f"mp4: {w}x{h} x{frames} frames: sides must be multiples of 16")
    sps, pps, units = h264(image, frames)
    video = [struct.pack(">I", len(u)) + u for u in units]  # 4-byte NAL lengths
    audio = [aac_silence()] * math.ceil(frames * RATE / (fps * AAC_FRAME))
    movie_ms = round(frames * 1000 / fps)
    avcc = box(b"avcC", avcc_payload(sps, pps))
    avc1 = box(
        b"avc1", bytes(6), struct.pack(">HHH3I", 1, 0, 0, 0, 0, 0), struct.pack(">HH", w, h),
        struct.pack(">IIIH", 0x480000, 0x480000, 0, 1), bytes(32), struct.pack(">Hh", 24, -1), avcc,
    )  # fmt: skip
    asc = bytes([0x11, 0x90])  # AudioSpecificConfig: AAC-LC, 48 kHz, 2 channels
    es = descriptor(
        3,
        struct.pack(">HB", 2, 0)
        + descriptor(4, struct.pack(">BB3sII", 0x40, 0x15, bytes(3), 0, 0) + descriptor(5, asc))
        + descriptor(6, b"\x02"),
    )
    mp4a = box(b"mp4a", bytes(6), struct.pack(">HII", 1, 0, 0),
               struct.pack(">HHHHI", 2, 16, 0, 0, RATE << 16), full(b"esds", 0, es))  # fmt: skip
    ftyp = box(b"ftyp", b"isom", struct.pack(">I", 512), b"isomiso2avc1mp41")
    vsize = sum(map(len, video))

    def moov(offset: int) -> bytes:
        mvhd = full(
            b"mvhd", 0, struct.pack(">IIII", 0, 0, 1000, movie_ms), struct.pack(">IH", 0x10000, 0x0100),
            bytes(10), MATRIX, bytes(24), struct.pack(">I", 3),
        )  # fmt: skip
        vtrak = trak(1, movie_ms, fps * 1000, frames * 1000, b"vide",
                     full(b"vmhd", 1, bytes(8)), avc1, 1000, [len(s) for s in video], offset, True)  # fmt: skip
        atrak = trak(2, movie_ms, RATE, len(audio) * AAC_FRAME, b"soun", full(b"smhd", 0, bytes(4)),
                     mp4a, AAC_FRAME, [len(s) for s in audio], offset + vsize, False)  # fmt: skip
        return box(b"moov", mvhd, vtrak, atrak)

    head = len(ftyp) + len(moov(0)) + 8  # the moov's size does not depend on the offsets
    return ftyp + moov(head) + box(b"mdat", *video, *audio)


if __name__ == "__main__":  # self-check: box layout and sizes
    clip = mp4(Image.new("RGB", (64, 32), (200, 40, 40)), 9, 24)
    assert clip[4:8] == b"ftyp" and clip.index(b"moov") < clip.index(b"mdat")
    assert len(aac_silence()) == 7
    print(f"ok: {len(clip)} bytes")

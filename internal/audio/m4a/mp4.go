package m4a

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os"
)

// ErrFormat means the file isn't an M4A/AAC stream this package reads.
var ErrFormat = errors.New("m4a: unsupported format")

// Meta is the parsed container metadata and frame index of an M4A or ADTS file.
type Meta struct {
	SampleRate   int
	Channels     int
	Bitrate      int
	Priming      int   // leading encoder delay samples to trim
	TotalSamples int64 // true audio frames (excluding priming & trailing padding)
	ASC          []byte
	Frames       []FrameEntry
}

// WriteM4A writes a standard fast-start MP4/M4A file (ftyp + moov + mdat)
// atomically by copying raw AAC frame payloads from src. No re-encoding occurs.
func WriteM4A(dstPath string, meta Meta, src io.ReaderAt) error {
	if meta.SampleRate <= 0 || meta.Channels <= 0 {
		return fmt.Errorf("m4a: invalid format %d Hz %d ch", meta.SampleRate, meta.Channels)
	}
	asc := meta.ASC
	if len(asc) < 2 {
		a, err := AudioSpecificConfig(meta.SampleRate, meta.Channels)
		if err != nil {
			return err
		}
		asc = a[:]
	}
	var mdatPayload int64
	for _, fr := range meta.Frames {
		mdatPayload += int64(fr.Size)
	}
	ftyp := box("ftyp", append([]byte("M4A \x00\x00\x00\x00M4A mp42isom"), nil...))
	// Build moov once with chunkOffset=0 to learn exact size, then patch stco.
	mdatHdrSize := int64(8)
	if mdatPayload+8 > 0xFFFFFFFF {
		mdatHdrSize = 16
	}
	moov0 := buildMoov(meta, asc, 0)
	chunkOffset := uint64(len(ftyp) + len(moov0) + int(mdatHdrSize))
	moov := buildMoov(meta, asc, chunkOffset)
	if len(moov) != len(moov0) {
		chunkOffset = uint64(len(ftyp) + len(moov) + int(mdatHdrSize))
		moov = buildMoov(meta, asc, chunkOffset)
	}

	tmp := dstPath + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	bw := bufio.NewWriterSize(f, 256<<10)
	writeErr := func(e error) error {
		f.Close()
		_ = os.Remove(tmp)
		return e
	}
	if _, err := bw.Write(ftyp); err != nil {
		return writeErr(err)
	}
	if _, err := bw.Write(moov); err != nil {
		return writeErr(err)
	}
	if mdatHdrSize == 8 {
		var h [8]byte
		binary.BigEndian.PutUint32(h[0:], uint32(8+mdatPayload))
		copy(h[4:], "mdat")
		if _, err := bw.Write(h[:]); err != nil {
			return writeErr(err)
		}
	} else {
		var h [16]byte
		binary.BigEndian.PutUint32(h[0:], 1)
		copy(h[4:], "mdat")
		binary.BigEndian.PutUint64(h[8:], uint64(16+mdatPayload))
		if _, err := bw.Write(h[:]); err != nil {
			return writeErr(err)
		}
	}
	var buf []byte
	for _, fr := range meta.Frames {
		if int(fr.Size) > cap(buf) {
			buf = make([]byte, fr.Size)
		}
		b := buf[:fr.Size]
		if _, err := src.ReadAt(b, fr.Offset); err != nil {
			return writeErr(err)
		}
		if _, err := bw.Write(b); err != nil {
			return writeErr(err)
		}
	}
	if err := bw.Flush(); err != nil {
		return writeErr(err)
	}
	if err := f.Sync(); err != nil {
		return writeErr(err)
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return err
	}
	return os.Rename(tmp, dstPath)
}

func buildMoov(meta Meta, asc []byte, chunkOffset uint64) []byte {
	rate := uint32(meta.SampleRate)
	total := uint64(max(0, meta.TotalSamples))
	encodedDur := uint64(len(meta.Frames)) * aacFrameSamples
	if total == 0 && encodedDur > uint64(meta.Priming) {
		total = encodedDur - uint64(meta.Priming)
	}
	// mvhd (version 1 for >12h duration safety at 48 kHz)
	var mvhd bytes.Buffer
	mvhd.Write([]byte{1, 0, 0, 0}) // version 1, flags 0
	putU64(&mvhd, 0)               // creation_time
	putU64(&mvhd, 0)               // modification_time
	putU32(&mvhd, rate)            // timescale
	putU64(&mvhd, total)           // duration
	putU32(&mvhd, 0x00010000)      // rate 1.0
	putU16(&mvhd, 0x0100)          // volume 1.0
	mvhd.Write(make([]byte, 10))   // reserved
	writeIdentityMatrix(&mvhd)
	mvhd.Write(make([]byte, 24)) // pre_defined
	putU32(&mvhd, 2)             // next_track_ID

	// tkhd (version 1, flags=3: enabled + in_movie)
	var tkhd bytes.Buffer
	tkhd.Write([]byte{1, 0, 0, 3})
	putU64(&tkhd, 0)     // creation
	putU64(&tkhd, 0)     // modification
	putU32(&tkhd, 1)     // track_ID
	putU32(&tkhd, 0)     // reserved
	putU64(&tkhd, total) // duration
	tkhd.Write(make([]byte, 8))
	putU16(&tkhd, 0)      // layer
	putU16(&tkhd, 1)      // alternate_group
	putU16(&tkhd, 0x0100) // volume 1.0
	putU16(&tkhd, 0)      // reserved
	writeIdentityMatrix(&tkhd)
	putU32(&tkhd, 0) // width
	putU32(&tkhd, 0) // height

	// edts -> elst (version 1): trims priming samples cleanly in compliant players
	var elst bytes.Buffer
	elst.Write([]byte{1, 0, 0, 0})
	putU32(&elst, 1)                    // entry_count
	putU64(&elst, total)                // segment_duration
	putU64(&elst, uint64(meta.Priming)) // media_time
	putU16(&elst, 1)                    // media_rate_integer
	putU16(&elst, 0)                    // media_rate_fraction
	edts := box("edts", box("elst", elst.Bytes()))

	// mdhd (version 1)
	var mdhd bytes.Buffer
	mdhd.Write([]byte{1, 0, 0, 0})
	putU64(&mdhd, 0)
	putU64(&mdhd, 0)
	putU32(&mdhd, rate)
	putU64(&mdhd, encodedDur)
	putU16(&mdhd, 0x55C4) // language "und"
	putU16(&mdhd, 0)

	// hdlr
	var hdlr bytes.Buffer
	hdlr.Write([]byte{0, 0, 0, 0})
	putU32(&hdlr, 0)
	hdlr.WriteString("soun")
	hdlr.Write(make([]byte, 12))
	hdlr.WriteString("SoundHandler\x00")

	// smhd
	smhd := box("smhd", make([]byte, 8))

	// dinf -> dref -> url
	var dref bytes.Buffer
	dref.Write([]byte{0, 0, 0, 0})
	putU32(&dref, 1)
	dref.Write(box("url ", []byte{0, 0, 0, 1})) // self-contained flag
	dinf := box("dinf", box("dref", dref.Bytes()))

	// stsd -> mp4a -> esds
	bitrate := uint32(meta.Bitrate)
	if bitrate == 0 {
		bitrate = 64000
	}
	var esds bytes.Buffer
	esds.Write([]byte{0, 0, 0, 0}) // version/flags
	decSpec := descr(0x05, asc)
	var decCfg bytes.Buffer
	decCfg.WriteByte(0x40) // MPEG-4 Audio
	decCfg.WriteByte(0x15) // audioStream (5<<2 | 1)
	decCfg.Write([]byte{0, 0, 0})
	putU32(&decCfg, bitrate)
	putU32(&decCfg, bitrate)
	decCfg.Write(decSpec)
	slCfg := descr(0x06, []byte{0x02})
	var es bytes.Buffer
	putU16(&es, 1) // ES_ID
	es.WriteByte(0)
	es.Write(descr(0x04, decCfg.Bytes()))
	es.Write(slCfg)
	esds.Write(descr(0x03, es.Bytes()))

	var mp4a bytes.Buffer
	mp4a.Write(make([]byte, 6)) // reserved
	putU16(&mp4a, 1)            // data_reference_index
	mp4a.Write(make([]byte, 8)) // reserved
	putU16(&mp4a, uint16(meta.Channels))
	putU16(&mp4a, 16)       // sample_size
	putU16(&mp4a, 0)        // pre_defined
	putU16(&mp4a, 0)        // reserved
	putU32(&mp4a, rate<<16) // sample_rate 16.16 fixed
	mp4a.Write(box("esds", esds.Bytes()))

	var stsd bytes.Buffer
	stsd.Write([]byte{0, 0, 0, 0})
	putU32(&stsd, 1)
	stsd.Write(box("mp4a", mp4a.Bytes()))

	// stts
	var stts bytes.Buffer
	stts.Write([]byte{0, 0, 0, 0})
	if len(meta.Frames) > 0 {
		putU32(&stts, 1)
		putU32(&stts, uint32(len(meta.Frames)))
		putU32(&stts, aacFrameSamples)
	} else {
		putU32(&stts, 0)
	}

	// stsc (1 chunk holding all frames)
	var stsc bytes.Buffer
	stsc.Write([]byte{0, 0, 0, 0})
	if len(meta.Frames) > 0 {
		putU32(&stsc, 1)
		putU32(&stsc, 1)
		putU32(&stsc, uint32(len(meta.Frames)))
		putU32(&stsc, 1)
	} else {
		putU32(&stsc, 0)
	}

	// stsz
	var stsz bytes.Buffer
	stsz.Write([]byte{0, 0, 0, 0})
	putU32(&stsz, 0)
	putU32(&stsz, uint32(len(meta.Frames)))
	for _, fr := range meta.Frames {
		putU32(&stsz, fr.Size)
	}

	// stco / co64
	var chunkBox []byte
	if chunkOffset <= 0xFFFFFFFF {
		var stco bytes.Buffer
		stco.Write([]byte{0, 0, 0, 0})
		if len(meta.Frames) > 0 {
			putU32(&stco, 1)
			putU32(&stco, uint32(chunkOffset))
		} else {
			putU32(&stco, 0)
		}
		chunkBox = box("stco", stco.Bytes())
	} else {
		var co64 bytes.Buffer
		co64.Write([]byte{0, 0, 0, 0})
		if len(meta.Frames) > 0 {
			putU32(&co64, 1)
			putU64(&co64, chunkOffset)
		} else {
			putU32(&co64, 0)
		}
		chunkBox = box("co64", co64.Bytes())
	}

	stbl := box("stbl", concat(
		box("stsd", stsd.Bytes()),
		box("stts", stts.Bytes()),
		box("stsc", stsc.Bytes()),
		box("stsz", stsz.Bytes()),
		chunkBox,
	))
	minf := box("minf", concat(smhd, dinf, stbl))
	mdia := box("mdia", concat(box("mdhd", mdhd.Bytes()), box("hdlr", hdlr.Bytes()), minf))
	trak := box("trak", concat(box("tkhd", tkhd.Bytes()), edts, mdia))
	return box("moov", concat(box("mvhd", mvhd.Bytes()), trak))
}

func box(typ string, body []byte) []byte {
	out := make([]byte, 8+len(body))
	binary.BigEndian.PutUint32(out[0:], uint32(len(out)))
	copy(out[4:], typ)
	copy(out[8:], body)
	return out
}

func descr(tag byte, body []byte) []byte {
	// ISO-14496-1 expandable size (1 byte when <128, else 4-byte 0x80-prefixed).
	if len(body) < 128 {
		out := make([]byte, 2+len(body))
		out[0], out[1] = tag, byte(len(body))
		copy(out[2:], body)
		return out
	}
	out := make([]byte, 5+len(body))
	out[0] = tag
	n := len(body)
	out[1] = byte(0x80 | ((n >> 21) & 0x7F))
	out[2] = byte(0x80 | ((n >> 14) & 0x7F))
	out[3] = byte(0x80 | ((n >> 7) & 0x7F))
	out[4] = byte(n & 0x7F)
	copy(out[5:], body)
	return out
}

func writeIdentityMatrix(b *bytes.Buffer) {
	putU32(b, 0x00010000)
	putU32(b, 0)
	putU32(b, 0)
	putU32(b, 0)
	putU32(b, 0x00010000)
	putU32(b, 0)
	putU32(b, 0)
	putU32(b, 0)
	putU32(b, 0x40000000)
}

func putU16(b *bytes.Buffer, v uint16) {
	var t [2]byte
	binary.BigEndian.PutUint16(t[:], v)
	b.Write(t[:])
}

func putU32(b *bytes.Buffer, v uint32) {
	var t [4]byte
	binary.BigEndian.PutUint32(t[:], v)
	b.Write(t[:])
}

func putU64(b *bytes.Buffer, v uint64) {
	var t [8]byte
	binary.BigEndian.PutUint64(t[:], v)
	b.Write(t[:])
}

func concat(parts ...[]byte) []byte {
	total := 0
	for _, p := range parts {
		total += len(p)
	}
	out := make([]byte, 0, total)
	for _, p := range parts {
		out = append(out, p...)
	}
	return out
}

// ParseM4A parses an ISO-BMFF M4A/MP4 audio file's moov box into Meta.
func ParseM4A(r io.ReaderAt, size int64) (Meta, error) {
	moovOff, moovSize, ok := findBox(r, 0, size, "moov")
	if !ok {
		return Meta{}, fmt.Errorf("%w: no moov box", ErrFormat)
	}
	moov := make([]byte, moovSize)
	if _, err := r.ReadAt(moov, moovOff); err != nil {
		return Meta{}, err
	}
	// Walk trak boxes inside moov to find the first sound track.
	var meta Meta
	var sizes []uint32
	type stscRow struct{ firstChunk, perChunk uint32 }
	var stsc []stscRow
	var chunks []int64
	var mdhdDur int64
	var elstDur int64

	walkBoxes(moov, func(typ string, body []byte) {
		if typ != "trak" || meta.SampleRate != 0 {
			return
		}
		var isAudio bool
		var m Meta
		var sz []uint32
		var sc []stscRow
		var ch []int64
		var md, ed int64

		walkBoxes(body, func(t2 string, b2 []byte) {
			switch t2 {
			case "edts":
				walkBoxes(b2, func(t3 string, b3 []byte) {
					if t3 == "elst" && len(b3) >= 8 {
						ver := b3[0]
						cnt := binary.BigEndian.Uint32(b3[4:])
						if cnt >= 1 {
							if ver == 1 && len(b3) >= 28 {
								ed = int64(binary.BigEndian.Uint64(b3[8:]))
								mt := int64(binary.BigEndian.Uint64(b3[16:]))
								if mt > 0 {
									m.Priming = int(mt)
								}
							} else if ver == 0 && len(b3) >= 20 {
								ed = int64(binary.BigEndian.Uint32(b3[8:]))
								mt := int32(binary.BigEndian.Uint32(b3[12:]))
								if mt > 0 {
									m.Priming = int(mt)
								}
							}
						}
					}
				})
			case "mdia":
				walkBoxes(b2, func(t3 string, b3 []byte) {
					switch t3 {
					case "mdhd":
						if len(b3) >= 24 {
							if b3[0] == 1 && len(b3) >= 32 {
								m.SampleRate = int(binary.BigEndian.Uint32(b3[20:]))
								md = int64(binary.BigEndian.Uint64(b3[24:]))
							} else if b3[0] == 0 {
								m.SampleRate = int(binary.BigEndian.Uint32(b3[12:]))
								md = int64(binary.BigEndian.Uint32(b3[16:]))
							}
						}
					case "hdlr":
						if len(b3) >= 12 && string(b3[8:12]) == "soun" {
							isAudio = true
						}
					case "minf":
						walkBoxes(b3, func(t4 string, b4 []byte) {
							if t4 != "stbl" {
								return
							}
							walkBoxes(b4, func(t5 string, b5 []byte) {
								switch t5 {
								case "stsd":
									if len(b5) >= 8 {
										walkBoxes(b5[8:], func(t6 string, b6 []byte) {
											if t6 == "mp4a" && len(b6) >= 28 {
												m.Channels = int(binary.BigEndian.Uint16(b6[16:]))
												if sr := int(binary.BigEndian.Uint32(b6[24:]) >> 16); sr > 0 && m.SampleRate == 0 {
													m.SampleRate = sr
												}
												walkBoxes(b6[28:], func(t7 string, b7 []byte) {
													if t7 == "esds" && len(b7) > 4 {
														m.ASC = parseESDS(b7[4:])
													}
												})
											}
										})
									}
								case "stsz":
									if len(b5) >= 12 {
										defSz := binary.BigEndian.Uint32(b5[4:])
										cnt := int(binary.BigEndian.Uint32(b5[8:]))
										if defSz != 0 {
											sz = make([]uint32, cnt)
											for i := range sz {
												sz[i] = defSz
											}
										} else if 12+4*cnt <= len(b5) {
											sz = make([]uint32, cnt)
											for i := range sz {
												sz[i] = binary.BigEndian.Uint32(b5[12+4*i:])
											}
										}
									}
								case "stsc":
									if len(b5) >= 8 {
										cnt := int(binary.BigEndian.Uint32(b5[4:]))
										for i := 0; i < cnt && 8+12*(i+1) <= len(b5); i++ {
											sc = append(sc, stscRow{
												firstChunk: binary.BigEndian.Uint32(b5[8+12*i:]),
												perChunk:   binary.BigEndian.Uint32(b5[12+12*i:]),
											})
										}
									}
								case "stco":
									if len(b5) >= 8 {
										cnt := int(binary.BigEndian.Uint32(b5[4:]))
										for i := 0; i < cnt && 8+4*(i+1) <= len(b5); i++ {
											ch = append(ch, int64(binary.BigEndian.Uint32(b5[8+4*i:])))
										}
									}
								case "co64":
									if len(b5) >= 8 {
										cnt := int(binary.BigEndian.Uint32(b5[4:]))
										for i := 0; i < cnt && 8+8*(i+1) <= len(b5); i++ {
											ch = append(ch, int64(binary.BigEndian.Uint64(b5[8+8*i:])))
										}
									}
								}
							})
						})
					}
				})
			}
		})
		if isAudio && m.SampleRate > 0 && m.Channels > 0 {
			meta, sizes, stsc, chunks, mdhdDur, elstDur = m, sz, sc, ch, md, ed
		}
	})
	if meta.SampleRate == 0 || meta.Channels == 0 {
		return Meta{}, fmt.Errorf("%w: no AAC audio track", ErrFormat)
	}
	// Reconstruct per-frame byte offsets from stsc + chunks + stsz.
	frames := make([]FrameEntry, 0, len(sizes))
	sIdx := 0
	for cIdx, cOff := range chunks {
		chunkNum := uint32(cIdx + 1)
		perChunk := uint32(0)
		for i := range stsc {
			if stsc[i].firstChunk <= chunkNum {
				perChunk = stsc[i].perChunk
			}
		}
		off := cOff
		for j := uint32(0); j < perChunk && sIdx < len(sizes); j++ {
			frames = append(frames, FrameEntry{Offset: off, Size: sizes[sIdx]})
			off += int64(sizes[sIdx])
			sIdx++
		}
	}
	meta.Frames = frames
	switch {
	case elstDur > 0:
		meta.TotalSamples = elstDur
	case mdhdDur > int64(meta.Priming):
		meta.TotalSamples = mdhdDur - int64(meta.Priming)
	default:
		meta.TotalSamples = max(0, int64(len(frames))*aacFrameSamples-int64(meta.Priming))
	}
	if len(meta.ASC) < 2 {
		if a, err := AudioSpecificConfig(meta.SampleRate, meta.Channels); err == nil {
			meta.ASC = a[:]
		}
	}
	return meta, nil
}

func findBox(r io.ReaderAt, start, end int64, want string) (bodyOff, bodySize int64, ok bool) {
	pos := start
	var hdr [16]byte
	for pos+8 <= end {
		if _, err := r.ReadAt(hdr[:8], pos); err != nil {
			return 0, 0, false
		}
		sz := int64(binary.BigEndian.Uint32(hdr[0:]))
		typ := string(hdr[4:8])
		hdrLen := int64(8)
		if sz == 1 {
			if pos+16 > end {
				return 0, 0, false
			}
			if _, err := r.ReadAt(hdr[8:16], pos+8); err != nil {
				return 0, 0, false
			}
			sz = int64(binary.BigEndian.Uint64(hdr[8:]))
			hdrLen = 16
		} else if sz == 0 {
			sz = end - pos
		}
		if sz < hdrLen || pos+sz > end {
			return 0, 0, false
		}
		if typ == want {
			return pos + hdrLen, sz - hdrLen, true
		}
		pos += sz
	}
	return 0, 0, false
}

func walkBoxes(b []byte, fn func(typ string, body []byte)) {
	pos := 0
	for pos+8 <= len(b) {
		sz := int(binary.BigEndian.Uint32(b[pos:]))
		typ := string(b[pos+4 : pos+8])
		hdr := 8
		if sz == 1 && pos+16 <= len(b) {
			sz = int(binary.BigEndian.Uint64(b[pos+8:]))
			hdr = 16
		} else if sz == 0 {
			sz = len(b) - pos
		}
		if sz < hdr || pos+sz > len(b) {
			break
		}
		fn(typ, b[pos+hdr:pos+sz])
		pos += sz
	}
}

// parseESDS finds the DecoderSpecificInfo (tag 0x05) inside an esds payload.
func parseESDS(b []byte) []byte {
	pos := 0
	readLen := func() (int, bool) {
		n := 0
		for range 4 {
			if pos >= len(b) {
				return 0, false
			}
			c := b[pos]
			pos++
			n = (n << 7) | int(c&0x7F)
			if c&0x80 == 0 {
				return n, true
			}
		}
		return n, true
	}
	for pos < len(b) {
		tag := b[pos]
		pos++
		sz, ok := readLen()
		if !ok || pos+sz > len(b) {
			return nil
		}
		switch tag {
		case 0x03: // ES_Descr
			if pos+3 > len(b) {
				return nil
			}
			flags := b[pos+2]
			pos += 3
			if flags&0x80 != 0 {
				pos += 2
			}
			if flags&0x40 != 0 && pos < len(b) {
				pos += 1 + int(b[pos])
			}
			if flags&0x20 != 0 {
				pos += 2
			}
		case 0x04: // DecoderConfigDescr
			if pos+13 > len(b) {
				return nil
			}
			pos += 13
		case 0x05: // DecoderSpecificInfo = AudioSpecificConfig
			out := make([]byte, sz)
			copy(out, b[pos:pos+sz])
			return out
		default:
			pos += sz
		}
	}
	return nil
}

// Recover remuxes a crash-leftover ADTS AAC file (aacPath) into a standard
// seekable M4A file (m4aPath) without re-encoding, and removes aacPath.
func Recover(aacPath, m4aPath string) error {
	f, err := os.Open(aacPath)
	if err != nil {
		return err
	}
	st, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	rate, ch, _, frames := scanADTS(f, st.Size(), 0, nil)
	if len(frames) == 0 || rate == 0 || ch == 0 {
		f.Close()
		return fmt.Errorf("%w: empty or unreadable ADTS stream", ErrFormat)
	}
	priming := defaultPriming
	total := max(int64(0), int64(len(frames))*aacFrameSamples-int64(priming))
	meta := Meta{
		SampleRate:   rate,
		Channels:     ch,
		Priming:      priming,
		TotalSamples: total,
		Frames:       frames,
	}
	err = WriteM4A(m4aPath, meta, f)
	f.Close()
	if err != nil {
		return err
	}
	_ = os.Remove(aacPath)
	return nil
}

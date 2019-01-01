/*
 * This file is subject to the terms and conditions defined in
 * file 'LICENSE.md', which is part of this source code package.
 */

package textencoding

import (
	"errors"
	"sort"
	"unicode/utf8"

	"github.com/unidoc/unidoc/common"
	"github.com/unidoc/unidoc/pdf/core"
	"golang.org/x/text/encoding"
	"golang.org/x/text/transform"
)

// SimpleEncoder represents a 1 byte encoding.
type SimpleEncoder interface {
	TextEncoder
	BaseName() string
	Charcodes() []CharCode
}

// NewCustomSimpleTextEncoder returns a simpleEncoder based on map `encoding` and difference map
// `differences`.
func NewCustomSimpleTextEncoder(encoding, differences map[CharCode]GlyphName) (SimpleEncoder, error) {
	if len(encoding) == 0 {
		return nil, errors.New("empty custom encoding")
	}
	const baseName = "custom"
	baseEncoding := make(map[byte]rune)
	for code, glyph := range encoding {
		r, ok := GlyphToRune(glyph)
		if !ok {
			common.Log.Debug("ERROR: Unknown glyph. %q", glyph)
			continue
		}
		baseEncoding[byte(code)] = r
	}
	// TODO(dennwc): this seems to be incorrect - baseEncoding won't be saved when converting to PDF object
	enc := newSimpleEncoderFromMap(baseName, baseEncoding)
	if len(differences) != 0 {
		enc = ApplyDifferences(enc, differences)
	}
	return enc, nil
}

// NewSimpleTextEncoder returns a simpleEncoder based on predefined encoding `baseName` and
// difference map `differences`.
func NewSimpleTextEncoder(baseName string, differences map[CharCode]GlyphName) (SimpleEncoder, error) {
	var enc SimpleEncoder
	if fnc, ok := simple[baseName]; ok {
		enc = fnc()
	} else {
		baseEncoding, ok := simpleEncodings[baseName]
		if !ok {
			common.Log.Debug("ERROR: NewSimpleTextEncoder. Unknown encoding %q", baseName)
			return nil, errors.New("unsupported font encoding")
		}
		// FIXME(dennwc): make a global and init once
		enc = newSimpleEncoderFromMap(baseName, baseEncoding)
	}
	if len(differences) != 0 {
		enc = ApplyDifferences(enc, differences)
	}
	return enc, nil
}

func newSimpleEncoderFromMap(name string, encoding map[byte]rune) SimpleEncoder {
	se := &simpleEncoding{
		baseName: name,
		decode:   encoding,
		encode:   make(map[rune]byte, len(encoding)),
	}
	for b, r := range se.decode {
		se.encode[r] = b
	}
	return se
}

var (
	simple = make(map[string]func() SimpleEncoder)
)

// RegisterSimpleEncoding registers a SimpleEncoder constructer by PDF encoding name.
func RegisterSimpleEncoding(name string, fnc func() SimpleEncoder) {
	if _, ok := simple[name]; ok {
		panic("already registered")
	}
	simple[name] = fnc
}

var (
	_ SimpleEncoder     = (*simpleEncoding)(nil)
	_ encoding.Encoding = (*simpleEncoding)(nil)
)

// simpleEncoding represents a 1 byte encoding.
type simpleEncoding struct {
	baseName string
	// one byte encoding: CharCode <-> byte
	encode map[rune]byte
	decode map[byte]rune
}

func (enc *simpleEncoding) Encode(raw string) []byte {
	data, _ := enc.NewEncoder().Bytes([]byte(raw))
	return data
}

// NewDecoder implements encoding.Encoding.
func (enc *simpleEncoding) NewDecoder() *encoding.Decoder {
	return &encoding.Decoder{Transformer: simpleDecoder{m: enc.decode}}
}

type simpleDecoder struct {
	m map[byte]rune
}

// Transform implements transform.Transformer.
func (enc simpleDecoder) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, _ error) {
	for len(src) != 0 {
		b := src[0]
		src = src[1:]

		r, ok := enc.m[b]
		if !ok {
			r = MissingCodeRune
		}
		if utf8.RuneLen(r) > len(dst) {
			return nDst, nSrc, transform.ErrShortDst
		}
		n := utf8.EncodeRune(dst, r)
		dst = dst[n:]

		nSrc++
		nDst += n
	}
	return nDst, nSrc, nil
}

// Reset implements transform.Transformer.
func (enc simpleDecoder) Reset() {}

// NewEncoder implements encoding.Encoding.
func (enc *simpleEncoding) NewEncoder() *encoding.Encoder {
	return &encoding.Encoder{Transformer: simpleEncoder{m: enc.encode}}
}

type simpleEncoder struct {
	m map[rune]byte
}

// Transform implements transform.Transformer.
func (enc simpleEncoder) Transform(dst, src []byte, atEOF bool) (nDst, nSrc int, _ error) {
	for len(src) != 0 {
		if !utf8.FullRune(src) && !atEOF {
			return nDst, nSrc, transform.ErrShortSrc
		} else if len(dst) == 0 {
			return nDst, nSrc, transform.ErrShortDst
		}
		r, n := utf8.DecodeRune(src)
		if r == utf8.RuneError {
			r = MissingCodeRune
		}
		src = src[n:]
		nSrc += n

		b, ok := enc.m[r]
		if !ok {
			b, _ = enc.m[MissingCodeRune]
		}
		dst[0] = b

		dst = dst[1:]
		nDst++
	}
	return nDst, nSrc, nil
}

// Reset implements transform.Transformer.
func (enc simpleEncoder) Reset() {}

// String returns a text representation of encoding.
func (enc *simpleEncoding) String() string {
	return "simpleEncoding(" + enc.baseName + ")"
}

// BaseName returns a base name of the encoder, as specified in the PDF spec.
func (enc *simpleEncoding) BaseName() string {
	return enc.baseName
}

func (enc *simpleEncoding) Charcodes() []CharCode {
	codes := make([]CharCode, 0, len(enc.decode))
	for b := range enc.decode {
		codes = append(codes, CharCode(b))
	}
	sort.Slice(codes, func(i, j int) bool {
		return codes[i] < codes[j]
	})
	return codes
}

func (enc *simpleEncoding) RuneToCharcode(r rune) (CharCode, bool) {
	b, ok := enc.encode[r]
	return CharCode(b), ok
}

func (enc *simpleEncoding) CharcodeToRune(code CharCode) (rune, bool) {
	if code > 0xff {
		return MissingCodeRune, false
	}
	b := byte(code)
	r, ok := enc.decode[b]
	return r, ok
}

func (enc *simpleEncoding) CharcodeToGlyph(code CharCode) (GlyphName, bool) {
	// TODO(dennwc): only redirects the call - remove from the interface
	r, ok := enc.CharcodeToRune(code)
	if !ok {
		return "", false
	}
	return enc.RuneToGlyph(r)
}

func (enc *simpleEncoding) GlyphToCharcode(glyph GlyphName) (CharCode, bool) {
	// TODO(dennwc): only redirects the call - remove from the interface
	r, ok := GlyphToRune(glyph)
	if !ok {
		return MissingCodeRune, false
	}
	return enc.RuneToCharcode(r)
}

func (enc *simpleEncoding) RuneToGlyph(r rune) (GlyphName, bool) {
	// TODO(dennwc): should be in the font interface
	return runeToGlyph(r, glyphlistRuneToGlyphMap)
}

func (enc *simpleEncoding) GlyphToRune(glyph GlyphName) (rune, bool) {
	// TODO(dennwc): should be in the font interface
	return glyphToRune(glyph, glyphlistGlyphToRuneMap)
}

func (enc *simpleEncoding) ToPdfObject() core.PdfObject {
	switch enc.baseName {
	case "MacRomanEncoding", "MacExpertEncoding", baseWinAnsi:
		return core.MakeName(enc.baseName)
	}
	// TODO(dennwc): check if this switch is necessary, or an old code was incorrect
	dict := core.MakeDict()
	dict.Set("Type", core.MakeName("Encoding"))
	dict.Set("BaseEncoding", core.MakeName(enc.baseName))
	dict.Set("Differences", toFontDifferences(nil))
	return core.MakeIndirectObject(dict)
}

// simpleEncodings is a map of the standard 8 bit character encodings.
var simpleEncodings = map[string]map[byte]rune{
	"MacExpertEncoding": { // 165 entries
		0x20: 0x0020, //    "space"
		0x21: 0xf721, //  "exclamsmall"
		0x22: 0xf6f8, //  "Hungarumlautsmall"
		0x23: 0xf7a2, //  "centoldstyle"
		0x24: 0xf724, //  "dollaroldstyle"
		0x25: 0xf6e4, //  "dollarsuperior"
		0x26: 0xf726, //  "ampersandsmall"
		0x27: 0xf7b4, //  "Acutesmall"
		0x28: 0x207d, //  ⁽ "parenleftsuperior"
		0x29: 0x207e, //  ⁾ "parenrightsuperior"
		0x2a: 0x2025, //  ‥ "twodotenleader"
		0x2b: 0x2024, //  ․ "onedotenleader"
		0x2c: 0x002c, //  , "comma"
		0x2d: 0x002d, //  - "hyphen"
		0x2e: 0x002e, //  . "period"
		0x2f: 0x2044, //  ⁄ "fraction"
		0x30: 0xf730, //  "zerooldstyle"
		0x31: 0xf731, //  "oneoldstyle"
		0x32: 0xf732, //  "twooldstyle"
		0x33: 0xf733, //  "threeoldstyle"
		0x34: 0xf734, //  "fouroldstyle"
		0x35: 0xf735, //  "fiveoldstyle"
		0x36: 0xf736, //  "sixoldstyle"
		0x37: 0xf737, //  "sevenoldstyle"
		0x38: 0xf738, //  "eightoldstyle"
		0x39: 0xf739, //  "nineoldstyle"
		0x3a: 0x003a, //  : "colon"
		0x3b: 0x003b, //  ; "semicolon"
		0x3d: 0xf6de, //  "threequartersemdash"
		0x3f: 0xf73f, //  "questionsmall"
		0x44: 0xf7f0, //  "Ethsmall"
		0x47: 0x00bc, //  ¼ "onequarter"
		0x48: 0x00bd, //  ½ "onehalf"
		0x49: 0x00be, //  ¾ "threequarters"
		0x4a: 0x215b, //  ⅛ "oneeighth"
		0x4b: 0x215c, //  ⅜ "threeeighths"
		0x4c: 0x215d, //  ⅝ "fiveeighths"
		0x4d: 0x215e, //  ⅞ "seveneighths"
		0x4e: 0x2153, //  ⅓ "onethird"
		0x4f: 0x2154, //  ⅔ "twothirds"
		0x56: 0xfb00, //  ﬀ "ff"
		0x57: 0xfb01, //  ﬁ "fi"
		0x58: 0xfb02, //  ﬂ "fl"
		0x59: 0xfb03, //  ﬃ "ffi"
		0x5a: 0xfb04, //  ﬄ "ffl"
		0x5b: 0x208d, //  ₍ "parenleftinferior"
		0x5d: 0x208e, //  ₎ "parenrightinferior"
		0x5e: 0xf6f6, //  "Circumflexsmall"
		0x5f: 0xf6e5, //  "hypheninferior"
		0x60: 0xf760, //  "Gravesmall"
		0x61: 0xf761, //  "Asmall"
		0x62: 0xf762, //  "Bsmall"
		0x63: 0xf763, //  "Csmall"
		0x64: 0xf764, //  "Dsmall"
		0x65: 0xf765, //  "Esmall"
		0x66: 0xf766, //  "Fsmall"
		0x67: 0xf767, //  "Gsmall"
		0x68: 0xf768, //  "Hsmall"
		0x69: 0xf769, //  "Ismall"
		0x6a: 0xf76a, //  "Jsmall"
		0x6b: 0xf76b, //  "Ksmall"
		0x6c: 0xf76c, //  "Lsmall"
		0x6d: 0xf76d, //  "Msmall"
		0x6e: 0xf76e, //  "Nsmall"
		0x6f: 0xf76f, //  "Osmall"
		0x70: 0xf770, //  "Psmall"
		0x71: 0xf771, //  "Qsmall"
		0x72: 0xf772, //  "Rsmall"
		0x73: 0xf773, //  "Ssmall"
		0x74: 0xf774, //  "Tsmall"
		0x75: 0xf775, //  "Usmall"
		0x76: 0xf776, //  "Vsmall"
		0x77: 0xf777, //  "Wsmall"
		0x78: 0xf778, //  "Xsmall"
		0x79: 0xf779, //  "Ysmall"
		0x7a: 0xf77a, //  "Zsmall"
		0x7b: 0x20a1, //  ₡ "colonmonetary"
		0x7c: 0xf6dc, //  "onefitted"
		0x7d: 0xf6dd, //  "rupiah"
		0x7e: 0xf6fe, //  "Tildesmall"
		0x81: 0xf6e9, //  "asuperior"
		0x82: 0xf6e0, //  "centsuperior"
		0x87: 0xf7e1, //  "Aacutesmall"
		0x88: 0xf7e0, //  "Agravesmall"
		0x89: 0xf7e2, //  "Acircumflexsmall"
		0x8a: 0xf7e4, //  "Adieresissmall"
		0x8b: 0xf7e3, //  "Atildesmall"
		0x8c: 0xf7e5, //  "Aringsmall"
		0x8d: 0xf7e7, //  "Ccedillasmall"
		0x8e: 0xf7e9, //  "Eacutesmall"
		0x8f: 0xf7e8, //  "Egravesmall"
		0x90: 0xf7ea, //  "Ecircumflexsmall"
		0x91: 0xf7eb, //  "Edieresissmall"
		0x92: 0xf7ed, //  "Iacutesmall"
		0x93: 0xf7ec, //  "Igravesmall"
		0x94: 0xf7ee, //  "Icircumflexsmall"
		0x95: 0xf7ef, //  "Idieresissmall"
		0x96: 0xf7f1, //  "Ntildesmall"
		0x97: 0xf7f3, //  "Oacutesmall"
		0x98: 0xf7f2, //  "Ogravesmall"
		0x99: 0xf7f4, //  "Ocircumflexsmall"
		0x9a: 0xf7f6, //  "Odieresissmall"
		0x9b: 0xf7f5, //  "Otildesmall"
		0x9c: 0xf7fa, //  "Uacutesmall"
		0x9d: 0xf7f9, //  "Ugravesmall"
		0x9e: 0xf7fb, //  "Ucircumflexsmall"
		0x9f: 0xf7fc, //  "Udieresissmall"
		0xa1: 0x2078, //  ⁸ "eightsuperior"
		0xa2: 0x2084, //  ₄ "fourinferior"
		0xa3: 0x2083, //  ₃ "threeinferior"
		0xa4: 0x2086, //  ₆ "sixinferior"
		0xa5: 0x2088, //  ₈ "eightinferior"
		0xa6: 0x2087, //  ₇ "seveninferior"
		0xa7: 0xf6fd, //  "Scaronsmall"
		0xa9: 0xf6df, //  "centinferior"
		0xaa: 0x2082, //  ₂ "twoinferior"
		0xac: 0xf7a8, //  "Dieresissmall"
		0xae: 0xf6f5, //  "Caronsmall"
		0xaf: 0xf6f0, //  "osuperior"
		0xb0: 0x2085, //  ₅ "fiveinferior"
		0xb2: 0xf6e1, //  "commainferior"
		0xb3: 0xf6e7, //  "periodinferior"
		0xb4: 0xf7fd, //  "Yacutesmall"
		0xb6: 0xf6e3, //  "dollarinferior"
		0xb9: 0xf7fe, //  "Thornsmall"
		0xbb: 0x2089, //  ₉ "nineinferior"
		0xbc: 0x2080, //  ₀ "zeroinferior"
		0xbd: 0xf6ff, //  "Zcaronsmall"
		0xbe: 0xf7e6, //  "AEsmall"
		0xbf: 0xf7f8, //  "Oslashsmall"
		0xc0: 0xf7bf, //  "questiondownsmall"
		0xc1: 0x2081, //  ₁ "oneinferior"
		0xc2: 0xf6f9, //  "Lslashsmall"
		0xc9: 0xf7b8, //  "Cedillasmall"
		0xcf: 0xf6fa, //  "OEsmall"
		0xd0: 0x2012, //  ‒ "figuredash"
		0xd1: 0xf6e6, //  "hyphensuperior"
		0xd6: 0xf7a1, //  "exclamdownsmall"
		0xd8: 0xf7ff, //  "Ydieresissmall"
		0xda: 0x00b9, //  ¹ "onesuperior"
		0xdb: 0x00b2, //  ² "twosuperior"
		0xdc: 0x00b3, //  ³ "threesuperior"
		0xdd: 0x2074, //  ⁴ "foursuperior"
		0xde: 0x2075, //  ⁵ "fivesuperior"
		0xdf: 0x2076, //  ⁶ "sixsuperior"
		0xe0: 0x2077, //  ⁷ "sevensuperior"
		0xe1: 0x2079, //  ⁹ "ninesuperior"
		0xe2: 0x2070, //  ⁰ "zerosuperior"
		0xe4: 0xf6ec, //  "esuperior"
		0xe5: 0xf6f1, //  "rsuperior"
		0xe6: 0xf6f3, //  "tsuperior"
		0xe9: 0xf6ed, //  "isuperior"
		0xea: 0xf6f2, //  "ssuperior"
		0xeb: 0xf6eb, //  "dsuperior"
		0xf1: 0xf6ee, //  "lsuperior"
		0xf2: 0xf6fb, //  "Ogoneksmall"
		0xf3: 0xf6f4, //  "Brevesmall"
		0xf4: 0xf7af, //  "Macronsmall"
		0xf5: 0xf6ea, //  "bsuperior"
		0xf6: 0x207f, //  ⁿ "nsuperior"
		0xf7: 0xf6ef, //  "msuperior"
		0xf8: 0xf6e2, //  "commasuperior"
		0xf9: 0xf6e8, //  "periodsuperior"
		0xfa: 0xf6f7, //  "Dotaccentsmall"
		0xfb: 0xf6fc, //  "Ringsmall"
	},
	"MacRomanEncoding": { // 255 entries
		0x01: 0x0001, //  "controlSTX"
		0x02: 0x0002, //  "controlSOT"
		0x03: 0x0003, //  "controlETX"
		0x04: 0x0004, //  "controlEOT"
		0x05: 0x0005, //  "controlENQ"
		0x06: 0x0006, //  "controlACK"
		0x07: 0x0007, //  "controlBEL"
		0x08: 0x0008, //  "controlBS"
		0x09: 0x0009, //  "controlHT"
		0x0a: 0x000a, //  "controlLF"
		0x0b: 0x000b, //  "controlVT"
		0x0c: 0x000c, //  "controlFF"
		0x0d: 0x000d, //  "controlCR"
		0x0e: 0x000e, //  "controlSO"
		0x0f: 0x000f, //  "controlSI"
		0x10: 0x0010, //  "controlDLE"
		0x11: 0x0011, //  "controlDC1"
		0x12: 0x0012, //  "controlDC2"
		0x13: 0x0013, //  "controlDC3"
		0x14: 0x0014, //  "controlDC4"
		0x15: 0x0015, //  "controlNAK"
		0x16: 0x0016, //  "controlSYN"
		0x17: 0x0017, //  "controlETB"
		0x18: 0x0018, //  "controlCAN"
		0x19: 0x0019, //  "controlEM"
		0x1a: 0x001a, //  "controlSUB"
		0x1b: 0x001b, //  "controlESC"
		0x1c: 0x001c, //  "controlFS"
		0x1d: 0x001d, //  "controlGS"
		0x1e: 0x001e, //  "controlRS"
		0x1f: 0x001f, //  "controlUS"
		0x20: 0x0020, //    "space"
		0x21: 0x0021, //  ! "exclam"
		0x22: 0x0022, //  " "quotedbl"
		0x23: 0x0023, //  # "numbersign"
		0x24: 0x0024, //  $ "dollar"
		0x25: 0x0025, //  % "percent"
		0x26: 0x0026, //  & "ampersand"
		0x27: 0x0027, //  \' "quotesingle"
		0x28: 0x0028, //  ( "parenleft"
		0x29: 0x0029, //  ) "parenright"
		0x2a: 0x002a, //  * "asterisk"
		0x2b: 0x002b, //  + "plus"
		0x2c: 0x002c, //  , "comma"
		0x2d: 0x002d, //  - "hyphen"
		0x2e: 0x002e, //  . "period"
		0x2f: 0x002f, //  / "slash"
		0x30: 0x0030, //  0 "zero"
		0x31: 0x0031, //  1 "one"
		0x32: 0x0032, //  2 "two"
		0x33: 0x0033, //  3 "three"
		0x34: 0x0034, //  4 "four"
		0x35: 0x0035, //  5 "five"
		0x36: 0x0036, //  6 "six"
		0x37: 0x0037, //  7 "seven"
		0x38: 0x0038, //  8 "eight"
		0x39: 0x0039, //  9 "nine"
		0x3a: 0x003a, //  : "colon"
		0x3b: 0x003b, //  ; "semicolon"
		0x3c: 0x003c, //  < "less"
		0x3d: 0x003d, //  = "equal"
		0x3e: 0x003e, //  > "greater"
		0x3f: 0x003f, //  ? "question"
		0x40: 0x0040, //  @ "at"
		0x41: 0x0041, //  A "A"
		0x42: 0x0042, //  B "B"
		0x43: 0x0043, //  C "C"
		0x44: 0x0044, //  D "D"
		0x45: 0x0045, //  E "E"
		0x46: 0x0046, //  F "F"
		0x47: 0x0047, //  G "G"
		0x48: 0x0048, //  H "H"
		0x49: 0x0049, //  I "I"
		0x4a: 0x004a, //  J "J"
		0x4b: 0x004b, //  K "K"
		0x4c: 0x004c, //  L "L"
		0x4d: 0x004d, //  M "M"
		0x4e: 0x004e, //  N "N"
		0x4f: 0x004f, //  O "O"
		0x50: 0x0050, //  P "P"
		0x51: 0x0051, //  Q "Q"
		0x52: 0x0052, //  R "R"
		0x53: 0x0053, //  S "S"
		0x54: 0x0054, //  T "T"
		0x55: 0x0055, //  U "U"
		0x56: 0x0056, //  V "V"
		0x57: 0x0057, //  W "W"
		0x58: 0x0058, //  X "X"
		0x59: 0x0059, //  Y "Y"
		0x5a: 0x005a, //  Z "Z"
		0x5b: 0x005b, //  [ "bracketleft"
		0x5c: 0x005c, //  \\ "backslash"
		0x5d: 0x005d, //  ] "bracketright"
		0x5e: 0x005e, //  ^ "asciicircum"
		0x5f: 0x005f, //  _ "underscore"
		0x60: 0x0060, //  ` "grave"
		0x61: 0x0061, //  a "a"
		0x62: 0x0062, //  b "b"
		0x63: 0x0063, //  c "c"
		0x64: 0x0064, //  d "d"
		0x65: 0x0065, //  e "e"
		0x66: 0x0066, //  f "f"
		0x67: 0x0067, //  g "g"
		0x68: 0x0068, //  h "h"
		0x69: 0x0069, //  i "i"
		0x6a: 0x006a, //  j "j"
		0x6b: 0x006b, //  k "k"
		0x6c: 0x006c, //  l "l"
		0x6d: 0x006d, //  m "m"
		0x6e: 0x006e, //  n "n"
		0x6f: 0x006f, //  o "o"
		0x70: 0x0070, //  p "p"
		0x71: 0x0071, //  q "q"
		0x72: 0x0072, //  r "r"
		0x73: 0x0073, //  s "s"
		0x74: 0x0074, //  t "t"
		0x75: 0x0075, //  u "u"
		0x76: 0x0076, //  v "v"
		0x77: 0x0077, //  w "w"
		0x78: 0x0078, //  x "x"
		0x79: 0x0079, //  y "y"
		0x7a: 0x007a, //  z "z"
		0x7b: 0x007b, //  { "braceleft"
		0x7c: 0x007c, //  | "bar"
		0x7d: 0x007d, //  } "braceright"
		0x7e: 0x007e, //  ~ "asciitilde"
		0x7f: 0x007f, //  "controlDEL"
		0x80: 0x00c4, //  Ä "Adieresis"
		0x81: 0x00c5, //  Å "Aring"
		0x82: 0x00c7, //  Ç "Ccedilla"
		0x83: 0x00c9, //  É "Eacute"
		0x84: 0x00d1, //  Ñ "Ntilde"
		0x85: 0x00d6, //  Ö "Odieresis"
		0x86: 0x00dc, //  Ü "Udieresis"
		0x87: 0x00e1, //  á "aacute"
		0x88: 0x00e0, //  à "agrave"
		0x89: 0x00e2, //  â "acircumflex"
		0x8a: 0x00e4, //  ä "adieresis"
		0x8b: 0x00e3, //  ã "atilde"
		0x8c: 0x00e5, //  å "aring"
		0x8d: 0x00e7, //  ç "ccedilla"
		0x8e: 0x00e9, //  é "eacute"
		0x8f: 0x00e8, //  è "egrave"
		0x90: 0x00ea, //  ê "ecircumflex"
		0x91: 0x00eb, //  ë "edieresis"
		0x92: 0x00ed, //  í "iacute"
		0x93: 0x00ec, //  ì "igrave"
		0x94: 0x00ee, //  î "icircumflex"
		0x95: 0x00ef, //  ï "idieresis"
		0x96: 0x00f1, //  ñ "ntilde"
		0x97: 0x00f3, //  ó "oacute"
		0x98: 0x00f2, //  ò "ograve"
		0x99: 0x00f4, //  ô "ocircumflex"
		0x9a: 0x00f6, //  ö "odieresis"
		0x9b: 0x00f5, //  õ "otilde"
		0x9c: 0x00fa, //  ú "uacute"
		0x9d: 0x00f9, //  ù "ugrave"
		0x9e: 0x00fb, //  û "ucircumflex"
		0x9f: 0x00fc, //  ü "udieresis"
		0xa0: 0x2020, //  † "dagger"
		0xa1: 0x00b0, //  ° "degree"
		0xa2: 0x00a2, //  ¢ "cent"
		0xa3: 0x00a3, //  £ "sterling"
		0xa4: 0x00a7, //  § "section"
		0xa5: 0x2022, //  • "bullet"
		0xa6: 0x00b6, //  ¶ "paragraph"
		0xa7: 0x00df, //  ß "germandbls"
		0xa8: 0x00ae, //  ® "registered"
		0xa9: 0x00a9, //  © "copyright"
		0xaa: 0x2122, //  ™ "trademark"
		0xab: 0x00b4, //  ´ "acute"
		0xac: 0x00a8, //  ¨ "dieresis"
		0xad: 0x2260, //  ≠ "notequal"
		0xae: 0x00c6, //  Æ "AE"
		0xaf: 0x00d8, //  Ø "Oslash"
		0xb0: 0x221e, //  ∞ "infinity"
		0xb1: 0x00b1, //  ± "plusminus"
		0xb2: 0x2264, //  ≤ "lessequal"
		0xb3: 0x2265, //  ≥ "greaterequal"
		0xb4: 0x00a5, //  ¥ "yen"
		0xb5: 0x00b5, //  µ "mu"
		0xb6: 0x2202, //  ∂ "partialdiff"
		0xb7: 0x2211, //  ∑ "summation"
		0xb8: 0x220f, //  ∏ "product"
		0xb9: 0x03c0, //  π "pi"
		0xba: 0x222b, //  ∫ "integral"
		0xbb: 0x00aa, //  ª "ordfeminine"
		0xbc: 0x00ba, //  º "ordmasculine"
		0xbd: 0x03a9, //  Ω "Omegagreek"
		0xbe: 0x00e6, //  æ "ae"
		0xbf: 0x00f8, //  ø "oslash"
		0xc0: 0x00bf, //  ¿ "questiondown"
		0xc1: 0x00a1, //  ¡ "exclamdown"
		0xc2: 0x00ac, //  ¬ "logicalnot"
		0xc3: 0x221a, //  √ "radical"
		0xc4: 0x0192, //  ƒ "florin"
		0xc5: 0x2248, //  ≈ "approxequal"
		0xc6: 0x2206, //  ∆ "Delta"
		0xc7: 0x00ab, //  « "guillemotleft"
		0xc8: 0x00bb, //  » "guillemotright"
		0xc9: 0x2026, //  … "ellipsis"
		0xca: 0x00a0, //  "nbspace"
		0xcb: 0x00c0, //  À "Agrave"
		0xcc: 0x00c3, //  Ã "Atilde"
		0xcd: 0x00d5, //  Õ "Otilde"
		0xce: 0x0152, //  Œ "OE"
		0xcf: 0x0153, //  œ "oe"
		0xd0: 0x2013, //  – "endash"
		0xd1: 0x2014, //  — "emdash"
		0xd2: 0x201c, //  “ "quotedblleft"
		0xd3: 0x201d, //  ” "quotedblright"
		0xd4: 0x2018, //  ‘ "quoteleft"
		0xd5: 0x2019, //  ’ "quoteright"
		0xd6: 0x00f7, //  ÷ "divide"
		0xd7: 0x25ca, //  ◊ "lozenge"
		0xd8: 0x00ff, //  ÿ "ydieresis"
		0xd9: 0x0178, //  Ÿ "Ydieresis"
		0xda: 0x2044, //  ⁄ "fraction"
		0xdb: 0x20ac, //  € "Euro"
		0xdc: 0x2039, //  ‹ "guilsinglleft"
		0xdd: 0x203a, //  › "guilsinglright"
		0xde: 0xfb01, //  ﬁ "fi"
		0xdf: 0xfb02, //  ﬂ "fl"
		0xe0: 0x2021, //  ‡ "daggerdbl"
		0xe1: 0x00b7, //  · "middot"
		0xe2: 0x201a, //  ‚ "quotesinglbase"
		0xe3: 0x201e, //  „ "quotedblbase"
		0xe4: 0x2030, //  ‰ "perthousand"
		0xe5: 0x00c2, //  Â "Acircumflex"
		0xe6: 0x00ca, //  Ê "Ecircumflex"
		0xe7: 0x00c1, //  Á "Aacute"
		0xe8: 0x00cb, //  Ë "Edieresis"
		0xe9: 0x00c8, //  È "Egrave"
		0xea: 0x00cd, //  Í "Iacute"
		0xeb: 0x00ce, //  Î "Icircumflex"
		0xec: 0x00cf, //  Ï "Idieresis"
		0xed: 0x00cc, //  Ì "Igrave"
		0xee: 0x00d3, //  Ó "Oacute"
		0xef: 0x00d4, //  Ô "Ocircumflex"
		0xf0: 0xf8ff, //  "apple"
		0xf1: 0x00d2, //  Ò "Ograve"
		0xf2: 0x00da, //  Ú "Uacute"
		0xf3: 0x00db, //  Û "Ucircumflex"
		0xf4: 0x00d9, //  Ù "Ugrave"
		0xf5: 0x0131, //  ı "dotlessi"
		0xf6: 0x02c6, //  ˆ "circumflex"
		0xf7: 0x02dc, //  ˜ "ilde"
		0xf8: 0x00af, //  ¯ "macron"
		0xf9: 0x02d8, //  ˘ "breve"
		0xfa: 0x02d9, //  ˙ "dotaccent"
		0xfb: 0x02da, //  ˚ "ring"
		0xfc: 0x00b8, //  ¸ "cedilla"
		0xfd: 0x02dd, //  ˝ "hungarumlaut"
		0xfe: 0x02db, //  ˛ "ogonek"
		0xff: 0x02c7, //  ˇ "caron"
	},
	"PdfDocEncoding": { // 252 entries
		0x01: 0x0001, //  "controlSTX"
		0x02: 0x0002, //  "controlSOT"
		0x03: 0x0003, //  "controlETX"
		0x04: 0x0004, //  "controlEOT"
		0x05: 0x0005, //  "controlENQ"
		0x06: 0x0006, //  "controlACK"
		0x07: 0x0007, //  "controlBEL"
		0x08: 0x0008, //  "controlBS"
		0x09: 0x0009, //  "controlHT"
		0x0a: 0x000a, //  "controlLF"
		0x0b: 0x000b, //  "controlVT"
		0x0c: 0x000c, //  "controlFF"
		0x0d: 0x000d, //  "controlCR"
		0x0e: 0x000e, //  "controlSO"
		0x0f: 0x000f, //  "controlSI"
		0x10: 0x0010, //  "controlDLE"
		0x11: 0x0011, //  "controlDC1"
		0x12: 0x0012, //  "controlDC2"
		0x13: 0x0013, //  "controlDC3"
		0x14: 0x0014, //  "controlDC4"
		0x15: 0x0015, //  "controlNAK"
		0x16: 0x0017, //  "controlETB"
		0x17: 0x0017, //  "controlETB"
		0x18: 0x02d8, //  ˘ "breve"
		0x19: 0x02c7, //  ˇ "caron"
		0x1a: 0x02c6, //  ˆ "circumflex"
		0x1b: 0x02d9, //  ˙ "dotaccent"
		0x1c: 0x02dd, //  ˝ "hungarumlaut"
		0x1d: 0x02db, //  ˛ "ogonek"
		0x1e: 0x02da, //  ˚ "ring"
		0x1f: 0x02dc, //  ˜ "ilde"
		0x20: 0x0020, //    "space"
		0x21: 0x0021, //  ! "exclam"
		0x22: 0x0022, //  " "quotedbl"
		0x23: 0x0023, //  # "numbersign"
		0x24: 0x0024, //  $ "dollar"
		0x25: 0x0025, //  % "percent"
		0x26: 0x0026, //  & "ampersand"
		0x27: 0x0027, //  \' "quotesingle"
		0x28: 0x0028, //  ( "parenleft"
		0x29: 0x0029, //  ) "parenright"
		0x2a: 0x002a, //  * "asterisk"
		0x2b: 0x002b, //  + "plus"
		0x2c: 0x002c, //  , "comma"
		0x2d: 0x002d, //  - "hyphen"
		0x2e: 0x002e, //  . "period"
		0x2f: 0x002f, //  / "slash"
		0x30: 0x0030, //  0 "zero"
		0x31: 0x0031, //  1 "one"
		0x32: 0x0032, //  2 "two"
		0x33: 0x0033, //  3 "three"
		0x34: 0x0034, //  4 "four"
		0x35: 0x0035, //  5 "five"
		0x36: 0x0036, //  6 "six"
		0x37: 0x0037, //  7 "seven"
		0x38: 0x0038, //  8 "eight"
		0x39: 0x0039, //  9 "nine"
		0x3a: 0x003a, //  : "colon"
		0x3b: 0x003b, //  ; "semicolon"
		0x3c: 0x003c, //  < "less"
		0x3d: 0x003d, //  = "equal"
		0x3e: 0x003e, //  > "greater"
		0x3f: 0x003f, //  ? "question"
		0x40: 0x0040, //  @ "at"
		0x41: 0x0041, //  A "A"
		0x42: 0x0042, //  B "B"
		0x43: 0x0043, //  C "C"
		0x44: 0x0044, //  D "D"
		0x45: 0x0045, //  E "E"
		0x46: 0x0046, //  F "F"
		0x47: 0x0047, //  G "G"
		0x48: 0x0048, //  H "H"
		0x49: 0x0049, //  I "I"
		0x4a: 0x004a, //  J "J"
		0x4b: 0x004b, //  K "K"
		0x4c: 0x004c, //  L "L"
		0x4d: 0x004d, //  M "M"
		0x4e: 0x004e, //  N "N"
		0x4f: 0x004f, //  O "O"
		0x50: 0x0050, //  P "P"
		0x51: 0x0051, //  Q "Q"
		0x52: 0x0052, //  R "R"
		0x53: 0x0053, //  S "S"
		0x54: 0x0054, //  T "T"
		0x55: 0x0055, //  U "U"
		0x56: 0x0056, //  V "V"
		0x57: 0x0057, //  W "W"
		0x58: 0x0058, //  X "X"
		0x59: 0x0059, //  Y "Y"
		0x5a: 0x005a, //  Z "Z"
		0x5b: 0x005b, //  [ "bracketleft"
		0x5c: 0x005c, //  \\ "backslash"
		0x5d: 0x005d, //  ] "bracketright"
		0x5e: 0x005e, //  ^ "asciicircum"
		0x5f: 0x005f, //  _ "underscore"
		0x60: 0x0060, //  ` "grave"
		0x61: 0x0061, //  a "a"
		0x62: 0x0062, //  b "b"
		0x63: 0x0063, //  c "c"
		0x64: 0x0064, //  d "d"
		0x65: 0x0065, //  e "e"
		0x66: 0x0066, //  f "f"
		0x67: 0x0067, //  g "g"
		0x68: 0x0068, //  h "h"
		0x69: 0x0069, //  i "i"
		0x6a: 0x006a, //  j "j"
		0x6b: 0x006b, //  k "k"
		0x6c: 0x006c, //  l "l"
		0x6d: 0x006d, //  m "m"
		0x6e: 0x006e, //  n "n"
		0x6f: 0x006f, //  o "o"
		0x70: 0x0070, //  p "p"
		0x71: 0x0071, //  q "q"
		0x72: 0x0072, //  r "r"
		0x73: 0x0073, //  s "s"
		0x74: 0x0074, //  t "t"
		0x75: 0x0075, //  u "u"
		0x76: 0x0076, //  v "v"
		0x77: 0x0077, //  w "w"
		0x78: 0x0078, //  x "x"
		0x79: 0x0079, //  y "y"
		0x7a: 0x007a, //  z "z"
		0x7b: 0x007b, //  { "braceleft"
		0x7c: 0x007c, //  | "bar"
		0x7d: 0x007d, //  } "braceright"
		0x7e: 0x007e, //  ~ "asciitilde"
		0x80: 0x2022, //  • "bullet"
		0x81: 0x2020, //  † "dagger"
		0x82: 0x2021, //  ‡ "daggerdbl"
		0x83: 0x2026, //  … "ellipsis"
		0x84: 0x2014, //  — "emdash"
		0x85: 0x2013, //  – "endash"
		0x86: 0x0192, //  ƒ "florin"
		0x87: 0x2044, //  ⁄ "fraction"
		0x88: 0x2039, //  ‹ "guilsinglleft"
		0x89: 0x203a, //  › "guilsinglright"
		0x8a: 0x2212, //  − "minus"
		0x8b: 0x2030, //  ‰ "perthousand"
		0x8c: 0x201e, //  „ "quotedblbase"
		0x8d: 0x201c, //  “ "quotedblleft"
		0x8e: 0x201d, //  ” "quotedblright"
		0x8f: 0x2018, //  ‘ "quoteleft"
		0x90: 0x2019, //  ’ "quoteright"
		0x91: 0x201a, //  ‚ "quotesinglbase"
		0x92: 0x2122, //  ™ "trademark"
		0x93: 0xfb01, //  ﬁ "fi"
		0x94: 0xfb02, //  ﬂ "fl"
		0x95: 0x0141, //  Ł "Lslash"
		0x96: 0x0152, //  Œ "OE"
		0x97: 0x0160, //  Š "Scaron"
		0x98: 0x0178, //  Ÿ "Ydieresis"
		0x99: 0x017d, //  Ž "Zcaron"
		0x9a: 0x0131, //  ı "dotlessi"
		0x9b: 0x0142, //  ł "lslash"
		0x9c: 0x0153, //  œ "oe"
		0x9d: 0x0161, //  š "scaron"
		0x9e: 0x017e, //  ž "zcaron"
		0xa0: 0x20ac, //  € "Euro"
		0xa1: 0x00a1, //  ¡ "exclamdown"
		0xa2: 0x00a2, //  ¢ "cent"
		0xa3: 0x00a3, //  £ "sterling"
		0xa4: 0x00a4, //  ¤ "currency"
		0xa5: 0x00a5, //  ¥ "yen"
		0xa6: 0x00a6, //  ¦ "brokenbar"
		0xa7: 0x00a7, //  § "section"
		0xa8: 0x00a8, //  ¨ "dieresis"
		0xa9: 0x00a9, //  © "copyright"
		0xaa: 0x00aa, //  ª "ordfeminine"
		0xab: 0x00ab, //  « "guillemotleft"
		0xac: 0x00ac, //  ¬ "logicalnot"
		0xae: 0x00ae, //  ® "registered"
		0xaf: 0x00af, //  ¯ "macron"
		0xb0: 0x00b0, //  ° "degree"
		0xb1: 0x00b1, //  ± "plusminus"
		0xb2: 0x00b2, //  ² "twosuperior"
		0xb3: 0x00b3, //  ³ "threesuperior"
		0xb4: 0x00b4, //  ´ "acute"
		0xb5: 0x00b5, //  µ "mu"
		0xb6: 0x00b6, //  ¶ "paragraph"
		0xb7: 0x00b7, //  · "middot"
		0xb8: 0x00b8, //  ¸ "cedilla"
		0xb9: 0x00b9, //  ¹ "onesuperior"
		0xba: 0x00ba, //  º "ordmasculine"
		0xbb: 0x00bb, //  » "guillemotright"
		0xbc: 0x00bc, //  ¼ "onequarter"
		0xbd: 0x00bd, //  ½ "onehalf"
		0xbe: 0x00be, //  ¾ "threequarters"
		0xbf: 0x00bf, //  ¿ "questiondown"
		0xc0: 0x00c0, //  À "Agrave"
		0xc1: 0x00c1, //  Á "Aacute"
		0xc2: 0x00c2, //  Â "Acircumflex"
		0xc3: 0x00c3, //  Ã "Atilde"
		0xc4: 0x00c4, //  Ä "Adieresis"
		0xc5: 0x00c5, //  Å "Aring"
		0xc6: 0x00c6, //  Æ "AE"
		0xc7: 0x00c7, //  Ç "Ccedilla"
		0xc8: 0x00c8, //  È "Egrave"
		0xc9: 0x00c9, //  É "Eacute"
		0xca: 0x00ca, //  Ê "Ecircumflex"
		0xcb: 0x00cb, //  Ë "Edieresis"
		0xcc: 0x00cc, //  Ì "Igrave"
		0xcd: 0x00cd, //  Í "Iacute"
		0xce: 0x00ce, //  Î "Icircumflex"
		0xcf: 0x00cf, //  Ï "Idieresis"
		0xd0: 0x00d0, //  Ð "Eth"
		0xd1: 0x00d1, //  Ñ "Ntilde"
		0xd2: 0x00d2, //  Ò "Ograve"
		0xd3: 0x00d3, //  Ó "Oacute"
		0xd4: 0x00d4, //  Ô "Ocircumflex"
		0xd5: 0x00d5, //  Õ "Otilde"
		0xd6: 0x00d6, //  Ö "Odieresis"
		0xd7: 0x00d7, //  × "multiply"
		0xd8: 0x00d8, //  Ø "Oslash"
		0xd9: 0x00d9, //  Ù "Ugrave"
		0xda: 0x00da, //  Ú "Uacute"
		0xdb: 0x00db, //  Û "Ucircumflex"
		0xdc: 0x00dc, //  Ü "Udieresis"
		0xdd: 0x00dd, //  Ý "Yacute"
		0xde: 0x00de, //  Þ "Thorn"
		0xdf: 0x00df, //  ß "germandbls"
		0xe0: 0x00e0, //  à "agrave"
		0xe1: 0x00e1, //  á "aacute"
		0xe2: 0x00e2, //  â "acircumflex"
		0xe3: 0x00e3, //  ã "atilde"
		0xe4: 0x00e4, //  ä "adieresis"
		0xe5: 0x00e5, //  å "aring"
		0xe6: 0x00e6, //  æ "ae"
		0xe7: 0x00e7, //  ç "ccedilla"
		0xe8: 0x00e8, //  è "egrave"
		0xe9: 0x00e9, //  é "eacute"
		0xea: 0x00ea, //  ê "ecircumflex"
		0xeb: 0x00eb, //  ë "edieresis"
		0xec: 0x00ec, //  ì "igrave"
		0xed: 0x00ed, //  í "iacute"
		0xee: 0x00ee, //  î "icircumflex"
		0xef: 0x00ef, //  ï "idieresis"
		0xf0: 0x00f0, //  ð "eth"
		0xf1: 0x00f1, //  ñ "ntilde"
		0xf2: 0x00f2, //  ò "ograve"
		0xf3: 0x00f3, //  ó "oacute"
		0xf4: 0x00f4, //  ô "ocircumflex"
		0xf5: 0x00f5, //  õ "otilde"
		0xf6: 0x00f6, //  ö "odieresis"
		0xf7: 0x00f7, //  ÷ "divide"
		0xf8: 0x00f8, //  ø "oslash"
		0xf9: 0x00f9, //  ù "ugrave"
		0xfa: 0x00fa, //  ú "uacute"
		0xfb: 0x00fb, //  û "ucircumflex"
		0xfc: 0x00fc, //  ü "udieresis"
		0xfd: 0x00fd, //  ý "yacute"
		0xfe: 0x00fe, //  þ "thorn"
		0xff: 0x00ff, //  ÿ "ydieresis"
	},
	"StandardEncoding": { // 149 entries
		0x20: 0x0020, //    "space"
		0x21: 0x0021, //  ! "exclam"
		0x22: 0x0022, //  " "quotedbl"
		0x23: 0x0023, //  # "numbersign"
		0x24: 0x0024, //  $ "dollar"
		0x25: 0x0025, //  % "percent"
		0x26: 0x0026, //  & "ampersand"
		0x27: 0x2019, //  ’ "quoteright"
		0x28: 0x0028, //  ( "parenleft"
		0x29: 0x0029, //  ) "parenright"
		0x2a: 0x002a, //  * "asterisk"
		0x2b: 0x002b, //  + "plus"
		0x2c: 0x002c, //  , "comma"
		0x2d: 0x002d, //  - "hyphen"
		0x2e: 0x002e, //  . "period"
		0x2f: 0x002f, //  / "slash"
		0x30: 0x0030, //  0 "zero"
		0x31: 0x0031, //  1 "one"
		0x32: 0x0032, //  2 "two"
		0x33: 0x0033, //  3 "three"
		0x34: 0x0034, //  4 "four"
		0x35: 0x0035, //  5 "five"
		0x36: 0x0036, //  6 "six"
		0x37: 0x0037, //  7 "seven"
		0x38: 0x0038, //  8 "eight"
		0x39: 0x0039, //  9 "nine"
		0x3a: 0x003a, //  : "colon"
		0x3b: 0x003b, //  ; "semicolon"
		0x3c: 0x003c, //  < "less"
		0x3d: 0x003d, //  = "equal"
		0x3e: 0x003e, //  > "greater"
		0x3f: 0x003f, //  ? "question"
		0x40: 0x0040, //  @ "at"
		0x41: 0x0041, //  A "A"
		0x42: 0x0042, //  B "B"
		0x43: 0x0043, //  C "C"
		0x44: 0x0044, //  D "D"
		0x45: 0x0045, //  E "E"
		0x46: 0x0046, //  F "F"
		0x47: 0x0047, //  G "G"
		0x48: 0x0048, //  H "H"
		0x49: 0x0049, //  I "I"
		0x4a: 0x004a, //  J "J"
		0x4b: 0x004b, //  K "K"
		0x4c: 0x004c, //  L "L"
		0x4d: 0x004d, //  M "M"
		0x4e: 0x004e, //  N "N"
		0x4f: 0x004f, //  O "O"
		0x50: 0x0050, //  P "P"
		0x51: 0x0051, //  Q "Q"
		0x52: 0x0052, //  R "R"
		0x53: 0x0053, //  S "S"
		0x54: 0x0054, //  T "T"
		0x55: 0x0055, //  U "U"
		0x56: 0x0056, //  V "V"
		0x57: 0x0057, //  W "W"
		0x58: 0x0058, //  X "X"
		0x59: 0x0059, //  Y "Y"
		0x5a: 0x005a, //  Z "Z"
		0x5b: 0x005b, //  [ "bracketleft"
		0x5c: 0x005c, //  \\ "backslash"
		0x5d: 0x005d, //  ] "bracketright"
		0x5e: 0x005e, //  ^ "asciicircum"
		0x5f: 0x005f, //  _ "underscore"
		0x60: 0x0060, //  ` "grave"
		0x61: 0x0061, //  a "a"
		0x62: 0x0062, //  b "b"
		0x63: 0x0063, //  c "c"
		0x64: 0x0064, //  d "d"
		0x65: 0x0065, //  e "e"
		0x66: 0x0066, //  f "f"
		0x67: 0x0067, //  g "g"
		0x68: 0x0068, //  h "h"
		0x69: 0x0069, //  i "i"
		0x6a: 0x006a, //  j "j"
		0x6b: 0x006b, //  k "k"
		0x6c: 0x006c, //  l "l"
		0x6d: 0x006d, //  m "m"
		0x6e: 0x006e, //  n "n"
		0x6f: 0x006f, //  o "o"
		0x70: 0x0070, //  p "p"
		0x71: 0x0071, //  q "q"
		0x72: 0x0072, //  r "r"
		0x73: 0x0073, //  s "s"
		0x74: 0x0074, //  t "t"
		0x75: 0x0075, //  u "u"
		0x76: 0x0076, //  v "v"
		0x77: 0x0077, //  w "w"
		0x78: 0x0078, //  x "x"
		0x79: 0x0079, //  y "y"
		0x7a: 0x007a, //  z "z"
		0x7b: 0x007b, //  { "braceleft"
		0x7c: 0x007c, //  | "bar"
		0x7d: 0x007d, //  } "braceright"
		0x7e: 0x007e, //  ~ "asciitilde"
		0xa1: 0x00a1, //  ¡ "exclamdown"
		0xa2: 0x00a2, //  ¢ "cent"
		0xa3: 0x00a3, //  £ "sterling"
		0xa4: 0x2044, //  ⁄ "fraction"
		0xa5: 0x00a5, //  ¥ "yen"
		0xa6: 0x0192, //  ƒ "florin"
		0xa7: 0x00a7, //  § "section"
		0xa8: 0x00a4, //  ¤ "currency"
		0xa9: 0x0027, //  \' "quotesingle"
		0xaa: 0x201c, //  “ "quotedblleft"
		0xab: 0x00ab, //  « "guillemotleft"
		0xac: 0x2039, //  ‹ "guilsinglleft"
		0xad: 0x203a, //  › "guilsinglright"
		0xae: 0xfb01, //  ﬁ "fi"
		0xaf: 0xfb02, //  ﬂ "fl"
		0xb1: 0x2013, //  – "endash"
		0xb2: 0x2020, //  † "dagger"
		0xb3: 0x2021, //  ‡ "daggerdbl"
		0xb4: 0x00b7, //  · "middot"
		0xb6: 0x00b6, //  ¶ "paragraph"
		0xb7: 0x2022, //  • "bullet"
		0xb8: 0x201a, //  ‚ "quotesinglbase"
		0xb9: 0x201e, //  „ "quotedblbase"
		0xba: 0x201d, //  ” "quotedblright"
		0xbb: 0x00bb, //  » "guillemotright"
		0xbc: 0x2026, //  … "ellipsis"
		0xbd: 0x2030, //  ‰ "perthousand"
		0xbf: 0x00bf, //  ¿ "questiondown"
		0xc1: 0x0060, //  ` "grave"
		0xc2: 0x00b4, //  ´ "acute"
		0xc3: 0x02c6, //  ˆ "circumflex"
		0xc4: 0x02dc, //  ˜ "ilde"
		0xc5: 0x00af, //  ¯ "macron"
		0xc6: 0x02d8, //  ˘ "breve"
		0xc7: 0x02d9, //  ˙ "dotaccent"
		0xc8: 0x00a8, //  ¨ "dieresis"
		0xca: 0x02da, //  ˚ "ring"
		0xcb: 0x00b8, //  ¸ "cedilla"
		0xcc: 0x02dd, //  ˝ "hungarumlaut"
		0xcd: 0x02db, //  ˛ "ogonek"
		0xce: 0x02c7, //  ˇ "caron"
		0xcf: 0x2014, //  — "emdash"
		0xe0: 0x00c6, //  Æ "AE"
		0xe2: 0x00aa, //  ª "ordfeminine"
		0xe7: 0x0141, //  Ł "Lslash"
		0xe8: 0x00d8, //  Ø "Oslash"
		0xe9: 0x0152, //  Œ "OE"
		0xea: 0x00ba, //  º "ordmasculine"
		0xf0: 0x00e6, //  æ "ae"
		0xf5: 0x0131, //  ı "dotlessi"
		0xf7: 0x0142, //  ł "lslash"
		0xf8: 0x00f8, //  ø "oslash"
		0xf9: 0x0153, //  œ "oe"
		0xfa: 0x00df, //  ß "germandbls"
	},
}

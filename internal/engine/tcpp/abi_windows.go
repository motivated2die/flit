package tcpp

import "unsafe"

// Go mirrors of transcribe.h's public structs (v0.4.2, ABI f99ad4169374b8f3),
// laid out field for field. Go and the Windows x64 C ABI align these types
// the same way (int32 4, int64 and pointers 8, bool 1), so the padding
// matches. checkABI compares every size with the DLL's own
// transcribe_abi_struct_size at load time.

type backendInitParams struct {
	StructSize      uint64
	ArtifactDir     *byte
	AllowedBackends uint32
}

type deviceInfo struct {
	StructSize  uint64
	Name        *byte
	Description *byte
	Kind        *byte
	DeviceID    *byte
	MemoryTotal uint64
	MemoryFree  uint64
	DeviceType  int32
	_           uint32
}

type modelLoadParams struct {
	StructSize uint64
	Backend    int32 // transcribe_backend_request
	Device     uintptr
	NoMmap     bool
}

type sessionParams struct {
	StructSize uint64
	NThreads   int32
	KVType     int32
	NCtx       int32
}

type runParams struct {
	StructSize      uint64
	Task            int32
	Timestamps      int32
	PNC             int32
	ITN             int32
	Diarize         int32
	Language        *byte
	TargetLanguage  *byte
	KeepSpecialTags bool
	Family          uintptr // const struct transcribe_ext *
	SpecKDrafts     int32
	Vocabulary      **byte
	NVocabulary     int32
	Prompt          *byte
	Prefix          *byte
}

type capabilities struct {
	StructSize                uint64
	NativeSampleRate          int32
	NLanguages                int32
	Languages                 **byte
	MaxTimestampKind          int32
	SupportsLanguageDetect    bool
	SupportsTranslate         bool
	SupportsStreaming         bool
	SupportsSpecDecode        bool
	MaxAudioMS                int64
	NTranslateTargetLanguages int32
	TranslateTargetLanguages  **byte
}

type segment struct {
	StructSize uint64
	T0MS, T1MS int64
	FirstWord  int32
	NWords     int32
	FirstToken int32
	NTokens    int32
	Text       *byte
	SpeakerID  int32
}

type word struct {
	StructSize uint64
	T0MS, T1MS int64
	SegIndex   int32
	FirstToken int32
	NTokens    int32
	Text       *byte
}

type vadInfo struct {
	StructSize   uint64
	SampleRate   int32
	FrameSamples int32
}

type vadSessionParams struct {
	StructSize uint64
	NThreads   int32
}

type vadParams struct {
	StructSize   uint64
	Threshold    float64
	NegThreshold float64
	MinSpeechMS  int32
	MinSilenceMS int32
	SpeechPadMS  int32
	MaxSpeechMS  int32
}

type vadResult struct {
	StructSize uint64
	NProbs     int32
	FirstFrame int64
	NSegments  int32
}

type vadSegment struct {
	StructSize  uint64
	StartSample int64
	EndSample   int64
}

type diarizeInfo struct {
	StructSize  uint64
	SampleRate  int32
	MaxSpeakers int32
}

type diarizeSessionParams struct {
	StructSize uint64
	NThreads   int32
}

type diarizeParams struct {
	StructSize uint64
	Family     uintptr // const struct transcribe_ext *
}

type speakerSegment struct {
	StructSize uint64
	T0, T1     int64
	SpeakerID  int32
	P          float32
}

// ext mirrors struct transcribe_ext; sortformerExt the Sortformer run
// extension (include/transcribe/sortformer.h), whose size the DLL's init
// function writes into ext.Size (checked before use).
type ext struct {
	Size uint64
	Kind uint32
}

type sortformerExt struct {
	Ext    ext
	Preset int32
}

// transcribe_abi_struct ids.
const (
	abiModelLoadParams   = 0
	abiSessionParams     = 1
	abiRunParams         = 2
	abiCapabilities      = 4
	abiSegment           = 6
	abiWord              = 7
	abiExt               = 12
	abiDeviceInfo        = 13
	abiSpeakerSegment    = 14
	abiBackendInitParams = 15
	abiDiarizeInfo       = 16
	abiDiarizeSession    = 17
	abiDiarizeParams     = 18
	abiVADInfo           = 24
	abiVADSessionParams  = 25
	abiVADParams         = 26
	abiVADResult         = 27
	abiVADSegment        = 28
)

// abiStructs lists every mirrored struct with its Go size.
var abiStructs = []struct {
	id   uintptr
	name string
	size uintptr
}{
	{abiModelLoadParams, "transcribe_model_load_params", unsafe.Sizeof(modelLoadParams{})},
	{abiSessionParams, "transcribe_session_params", unsafe.Sizeof(sessionParams{})},
	{abiRunParams, "transcribe_run_params", unsafe.Sizeof(runParams{})},
	{abiCapabilities, "transcribe_capabilities", unsafe.Sizeof(capabilities{})},
	{abiSegment, "transcribe_segment", unsafe.Sizeof(segment{})},
	{abiWord, "transcribe_word", unsafe.Sizeof(word{})},
	{abiBackendInitParams, "transcribe_backend_init_params", unsafe.Sizeof(backendInitParams{})},
	{abiDeviceInfo, "transcribe_device_info", unsafe.Sizeof(deviceInfo{})},
	{abiExt, "transcribe_ext", unsafe.Sizeof(ext{})},
	{abiSpeakerSegment, "transcribe_speaker_segment", unsafe.Sizeof(speakerSegment{})},
	{abiDiarizeInfo, "transcribe_diarize_info", unsafe.Sizeof(diarizeInfo{})},
	{abiDiarizeSession, "transcribe_diarize_session_params", unsafe.Sizeof(diarizeSessionParams{})},
	{abiDiarizeParams, "transcribe_diarize_params", unsafe.Sizeof(diarizeParams{})},
	{abiVADInfo, "transcribe_vad_info", unsafe.Sizeof(vadInfo{})},
	{abiVADSessionParams, "transcribe_vad_session_params", unsafe.Sizeof(vadSessionParams{})},
	{abiVADParams, "transcribe_vad_params", unsafe.Sizeof(vadParams{})},
	{abiVADResult, "transcribe_vad_result", unsafe.Sizeof(vadResult{})},
	{abiVADSegment, "transcribe_vad_segment", unsafe.Sizeof(vadSegment{})},
}

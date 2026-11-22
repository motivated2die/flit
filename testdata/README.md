# Test data

## In git (small fixtures)

| File | Content | Source | License |
|---|---|---|---|
| `jfk.wav` | 11 s, English: "And so my fellow Americans, ask not what your country can do for you, ask what you can do for your country." | J. F. Kennedy's inaugural address (1961), as shipped in transcribe.cpp's `samples/` | Public domain (US government work) |
| `fleurs-de.wav` | 11 s, German: "Er griff auch alles an, was ins Wasser kam; selbst ein großer Dinosaurier wie der T. Rex war ihm nicht gewachsen." | [google/fleurs](https://huggingface.co/datasets/google/fleurs), `de_de` test split, utterance 1738, as shipped in transcribe.cpp's `samples/` | CC BY 4.0 |

Both are 16 kHz mono 16-bit WAV.

## Built locally (`go run ./cmd/flitdev testdata`, git-ignored)

`meetings/` holds test meetings with separate Me/Them tracks and references. What they are, how they're made and their licenses: [docs/testing/test-data.md](../docs/testing/test-data.md).

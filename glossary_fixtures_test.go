package subtrans_test

// Fixed multilingual subtitle fixtures used by the glossary end-to-end tests.
// Each fixture is deliberately small but exercises a different codec and a
// different arrangement of glossary terms.

const srtFixture = `1
00:00:01,000 --> 00:00:03,000
Aria met the Storm King at dawn.

2
00:00:03,500 --> 00:00:05,000
<i>The Storm King</i> said nothing.

3
00:00:05,500 --> 00:00:07,000
aria answered the storm king.

4
00:00:07,500 --> 00:00:09,000
Nobody else was there.
`

const vttFixture = `WEBVTT
Kind: captions
Language: en

NOTE opening scene
This note must survive translation.

cue-1
00:00:01.000 --> 00:00:03.000 align:start position:10%
Aria drew her blade.

00:00:03.500 --> 00:00:05.000
The Storm King laughed.
`

const assFixture = `[Script Info]
ScriptType: v4.00+
PlayResX: 1920
PlayResY: 1080

[V4+ Styles]
Format: Name, Fontname, Fontsize, PrimaryColour, SecondaryColour, OutlineColour, BackColour, Bold, Italic, Underline, StrikeOut, ScaleX, ScaleY, Spacing, Angle, BorderStyle, Outline, Shadow, Alignment, MarginL, MarginR, MarginV, Encoding
Style: Default,Arial,48,&H00FFFFFF,&H000000FF,&H00000000,&H00000000,0,0,0,0,100,100,0,0,1,2,2,2,10,10,30,1

[Events]
Format: Layer, Start, End, Style, Name, MarginL, MarginR, MarginV, Effect, Text
Dialogue: 0,0:00:01.00,0:00:03.00,Default,Aria,0,0,0,,{\b1}Aria{\b0} rides north.
Dialogue: 0,0:00:03.50,0:00:05.00,Default,,0,0,0,,The Storm King waits\Nfor Aria.
`

const lrcFixture = `[ti:Ballad of Aria]
[ar:The Storm King]

[00:01.00]Aria walked alone.
[00:03.50]The Storm King followed.
`

const sbvFixture = `0:00:01.000,0:00:03.000
Aria lit the beacon.

0:00:03.500,0:00:05.000
The Storm King saw it.
`

// glossaries maps target-language code to source→target term pairs. Multiple
// languages are used so the tests verify that glossary restoration is
// independent of the translation language.
var glossaries = map[string]map[string]string{
	"zh": {
		"Aria":       "阿莉娅",
		"Storm King": "风暴之王",
	},
	"ja": {
		"Aria":       "アリア",
		"Storm King": "嵐の王",
	},
	"fr": {
		"Aria":       "Arya",
		"Storm King": "Roi des Tempêtes",
	},
}

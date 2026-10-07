package main

// 场景音色合成器（t_146）：与 web/ui/sound.js 同规格——11025Hz 8-bit 单声道
// PCM WAV，脚本合成后 base64 内联。八枚场景音，全部「配角不抢戏」：
//   print   打印出纸「吱-吱」两声（r_03 §三预留基调）
//   pour    倒水汩汩（短促气泡群）
//   crunch  吃零食咔嚓（脆噪声两拍）
//   sweep   扫地刷刷（带通噪声扫频）
//   ding    任务落地轻叮（正弦短衰减）
//   pop     表情泡泡「啵」（正弦快速下滑）
//   whisper 摸鱼窃窃私语（低频气声，可选枚）
//   binlap  垃圾桶盖「啪」（短促敲击）
import (
	"encoding/base64"
	"fmt"
	"math"
	"math/rand"
	"os"
)

const SR = 11025

func synth(dur float64, fn func(t float64) float64) []byte {
	n := int(dur * SR)
	out := make([]byte, 44+n)
	copy(out, "RIFF")
	sz := uint32(36 + n)
	out[4], out[5], out[6], out[7] = byte(sz), byte(sz>>8), byte(sz>>16), byte(sz>>24)
	sr := uint32(SR)
	copy(out[8:], "WAVE")
	copy(out[12:], "fmt ")
	out[16], out[17] = 16, 0 // chunk size
	out[20], out[21] = 1, 0  // PCM
	out[22], out[23] = 1, 0  // mono
	out[24], out[25], out[26], out[27] = byte(sr), byte(sr>>8), byte(sr>>16), byte(sr>>24)
	bo := uint32(SR) // byte rate = SR * 1ch * 1byte
	out[28], out[29], out[30], out[31] = byte(bo), byte(bo>>8), byte(bo>>16), byte(bo>>24)
	out[32], out[33] = 1, 0 // block align
	out[34], out[35] = 8, 0 // bits
	copy(out[36:], "data")
	nn := uint32(n)
	out[40], out[41], out[42], out[43] = byte(nn), byte(nn>>8), byte(nn>>16), byte(nn>>24)
	for i := 0; i < n; i++ {
		t := float64(i) / SR
		v := fn(t)
		if v > 1 {
			v = 1
		}
		if v < -1 {
			v = -1
		}
		out[44+i] = byte(v*110 + 128)
	}
	return out
}

// env 包络（attack-decay）
func env(t, dur, a, r float64) float64 {
	if t < a {
		return t / a
	}
	if t > dur-r {
		return math.Max(0, (dur-t)/r)
	}
	return 1
}

func main() {
	rng := rand.New(rand.NewSource(42))
	if err := os.MkdirAll("/tmp/sfxgen", 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "mkdir /tmp/sfxgen: %v\n", err)
	}
	// print：出纸「吱-吱」——两段 2.2kHz 方波窄带摩擦声（步进出纸的机械吱声）
	print1 := func(t float64) float64 {
		sq := math.Signbit(math.Sin(2 * math.Pi * 2200 * t))
		v := 0.0
		if sq {
			v = 1
		} else {
			v = -1
		}
		// 两声：0–0.10s 与 0.16–0.26s
		if t < 0.10 {
			return v * env(t, 0.10, 0.005, 0.02) * 0.35
		}
		if t >= 0.16 && t < 0.26 {
			return v * env(t-0.16, 0.10, 0.005, 0.02) * 0.35
		}
		return 0
	}
	// pour：倒水汩汩——低频正弦（150→90Hz 滑）＋随机气泡群（2-4kHz 短促 blip）
	pour := func(t float64) float64 {
		if t > 0.5 {
			return 0
		}
		f := 150 - 120*t/0.5
		base := math.Sin(2*math.Pi*f*t) * 0.22 * env(t, 0.5, 0.03, 0.1)
		bub := 0.0
		// 确定性伪随机气泡：固定种子序列
		for i := 0; i < 9; i++ {
			bt := float64(i)*0.05 + 0.02
			bf := 2000 + float64(i%4)*600
			if t > bt && t < bt+0.018 {
				bub += math.Sin(2*math.Pi*bf*(t-bt)) * 0.12 * (1 - (t-bt)/0.018)
			}
		}
		return base + bub
	}
	_ = rng
	// crunch：咔嚓——两拍高通噪声（脆）
	crunch := func(t float64) float64 {
		cr := func(tt float64) float64 {
			r2 := rand.New(rand.NewSource(int64(tt * 1e6)))
			_ = r2
			return 0
		}
		_ = cr
		// 噪声用确定性哈希
		noise := func(x float64) float64 {
			s := math.Sin(x*127.1) * 43758.5453
			return s - math.Floor(s) - 0.5
		}
		if t < 0.07 {
			return noise(t*SR) * env(t, 0.07, 0.002, 0.02) * 0.5
		}
		if t >= 0.12 && t < 0.19 {
			return noise(t*SR) * env(t-0.12, 0.07, 0.002, 0.02) * 0.38
		}
		return 0
	}
	// sweep：扫地刷刷——噪声带扫（低→高→低），0.55s
	sweep := func(t float64) float64 {
		if t > 0.55 {
			return 0
		}
		noise := func(x float64) float64 {
			s := math.Sin(x*269.5) * 18343.456
			return s - math.Floor(s) - 0.5
		}
		cf := 400 + 900*math.Sin(math.Pi*t/0.55) // 中心频率扫
		// 简易带通：噪声与正弦载波相乘近似
		car := math.Sin(2 * math.Pi * cf * t)
		return noise(t*SR*7) * car * env(t, 0.55, 0.05, 0.12) * 0.3
	}
	// ding：任务落地轻叮——1318Hz（E6）正弦指数衰减 0.35s
	ding := func(t float64) float64 {
		if t > 0.35 {
			return 0
		}
		return math.Sin(2*math.Pi*1318.5*t) * math.Exp(-t*9) * 0.4
	}
	// pop：泡泡「啵」——800→300Hz 正弦快滑 0.09s
	pop := func(t float64) float64 {
		if t > 0.09 {
			return 0
		}
		f := 800 - 5500*t // 快滑
		if f < 280 {
			f = 280
		}
		return math.Sin(2*math.Pi*f*t) * env(t, 0.09, 0.004, 0.03) * 0.5
	}
	// whisper：窃窃私语——低频气声调制噪声 0.4s，极轻
	whisper := func(t float64) float64 {
		if t > 0.4 {
			return 0
		}
		noise := func(x float64) float64 {
			s := math.Sin(x*311.7) * 27123.123
			return s - math.Floor(s) - 0.5
		}
		mod := 0.5 + 0.5*math.Sin(2*math.Pi*6*t) // 说话节奏调制
		return noise(t*SR*3) * mod * env(t, 0.4, 0.08, 0.15) * 0.16
	}
	// binlap：桶盖「啪」——单拍敲击：1.5kHz 快衰减＋少量 300Hz 体腔
	binlap := func(t float64) float64 {
		if t > 0.12 {
			return 0
		}
		return (math.Sin(2*math.Pi*1500*t)*math.Exp(-t*60)*0.5 +
			math.Sin(2*math.Pi*300*t)*math.Exp(-t*40)*0.3)
	}

	sounds := map[string]func(float64) float64{
		"print": print1, "pour": pour, "crunch": crunch, "sweep": sweep,
		"ding": ding, "pop": pop, "whisper": whisper, "binlap": binlap,
	}
	durs := map[string]float64{
		"print": 0.30, "pour": 0.5, "crunch": 0.22, "sweep": 0.55,
		"ding": 0.35, "pop": 0.09, "whisper": 0.4, "binlap": 0.12,
	}
	for name, fn := range sounds {
		wav := synth(durs[name], fn)
		b64 := base64.StdEncoding.EncodeToString(wav)
		fmt.Printf("=== %s (%d bytes wav) ===\n", name, len(wav))
		// 76 列换行（sound.js 的内联格式），带 data URI 前缀
		uri := "data:audio/wav;base64," + b64
		for i := 0; i < len(uri); i += 76 {
			e := i + 76
			if e > len(uri) {
				e = len(uri)
			}
			fmt.Printf("%s\n", uri[i:e])
		}
		fmt.Println("---")
		// 写文件供检视
		if err := os.WriteFile("/tmp/sfxgen/"+name+".wav", wav, 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s.wav: %v\n", name, err)
		}
		if err := os.WriteFile("/tmp/sfxgen/"+name+".b64", []byte(b64), 0644); err != nil {
			fmt.Fprintf(os.Stderr, "write %s.b64: %v\n", name, err)
		}
	}
	fmt.Println("8 sounds synthesized to /tmp/sfxgen/")
}

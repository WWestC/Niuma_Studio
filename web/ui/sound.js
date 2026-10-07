// sound.js — 提示音中枢（声音交互篇，参考飞书／微信的消息提醒语言）。
//
// 三枚音色、各司其职（11025Hz 8-bit 单声道 PCM WAV 内联 base64，脚本
// 合成的真音频数据；HTMLAudio 直播——无音频栈的环境里 play() 被拒只是
// 一次可捕获的 rejection，绝不卡页，见 settings.js 的老注释）：
//   msg    微信式「叮咚」下行双音（A6→E6，第二音更低稍长）——新对话
//          消息的轻提醒，牛马你一言我一语时不抢戏
//   at     点名「叮铃」上行三连音（E6→A6→C#7）——有人@我，更亮更急，
//          与下行叮咚一听即分（飞书点名红标的听觉面）
//   banner 双音叮（E6→A6，原通知横幅音原样迁入）——公告等事件横幅
//
// 节流（防机枪）：调度器驱动的一回合汇报常是连珠炮，逐条响铃就是噪音
// ——同类音一枚冷却窗（msg 2.5s / at 1.2s / banner 2s），窗内到账的
// 消息交给弹窗合并计数，不再出声。试听（设置卡开关）带 force 跳窗。
//
// 总闸是设置卡的「提示音」开关（dh.ui.prefs 的 notifySound，微信式
// 单一声音开关管全部音源）；音量走同键族的 notifyVolume（0–100 整数、
// 缺省 50，设置卡滑杆直写，全部音色共用）。氛围音另有独立闸
// sceneSound（设置卡「办公室氛围音」）：世界常转，人在别的板块时
// 听得见办公室动静却看不见画面，关它不该陪葬消息叮咚。自动播放策略
// 下首个用户手势之前的 play() 会被拒——同样静默让行。
//
// 场景音（r_09，t_147/t_148）：八枚 ambient 音色为办公室的哑片配声——
// print 出纸吱吱 / pour 倒水汩汩 / crunch 吃零食咔嚓 / sweep 扫地刷刷 /
// ding 任务落地轻叮 / pop 表情泡泡啵 / whisper 摸鱼私语 / binlap 桶盖啪。
// 键名带 scene- 前缀（与消息音分命名空间，冷却互不顶替）；音量再乘
// SCENE_VOLUME=0.6（氛围垫底，聊天提醒永远是主角）；冷却窗更长（定稿
// §二表）；深夜 0–6 点全部场景音静音（真人口径护睡——模拟与阿姨夜班
// 照跑，只是不出声）。
// 配方真源 design/r09-sfx §一，合成器 tools/sfxgen（零资产纪律）。

import { prefs } from './settings.js';

// msg：微信式「叮咚」下行双音 A6→E6，约 0.30s
const MSG_URI =
  'data:audio/wav;base64,' +
  'UklGRg8NAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YesMAACAgoGAfnl7jIeDe3RukpCIe3JhjZ6MfnFZe6yS' +
  'hXBZYbSdjXFdSK6ulHVhOJXCmn9mPXDJn4pqS1DDqJJvWTyutZZ3YzmNwpqCaEJpx5+Ma1BMvqmScV09preWeWU8hcKahGlG' +
  'Y8SgjW1USbirk3NgPp+4lnxnQH3BmoZrS13AoY5uWEeyrJN1Yz+XuJZ+aUR2wJqHbFBZvKKPcFxGrK2Td2VBkLmWgGpIcL6b' +
  'iW1UVbijkHJgRqWuk3lnRIm5loJrTGq8m4pvWFKzpJBzY0afr5N7aUeCuJaEbVBluZyLcFxQrqWQdWVHmLCTfWtKfLiWhW5U' +
  'YbadjHFfT6mmkHdoSZKxk39sTXa2l4dvWF2znY1zYk6kp5F5akuLsZOBblFwtZeIcFtar56OdGVOnqiQe2tNhbGTgm9UbLOX' +
  'iXFfWKufjnZoT5ipkHxtUICwk4RwWGiwmIpzYlamoI53alCTqpB+blJ6sJOFcVtkrpmLdGVVoqGOeWxRjaqQgG9Vdq6UhnJe' +
  'Yauai3VnVZ2ijnptU4iqkIFwWHGtlIdzYV+nmox2alWYo458b1WDqpCCcVttq5SIdGRdpJuMeGxWk6OOfXBXfqmQhHJeaqmV' +
  'iXVnXKCcjHltV46kjn9xWnqpkYVzYWemlop2aVucnYx6b1iKpI6Aclx2qJGGdGRlpJaKd2tbl56MfHBahaSOgXNfcqaRh3Vn' +
  'Y6GXinhtW5OejH1yXIGkjoN0YW+kkod2aWGdmIp5b1yPn4x+c159o46EdWRsopKId2thmpiKenFdi5+Mf3NgeaOOhHVmaqCT' +
  'iHhtYJeZi3tyXoefjIB0Ynaij4V2aWiek4l5b2CTmot9c2CDn4yCdWRzoI+Gd2tmm5SJeXBgj5qKfnRhgJ+MgnZmcJ+Phndt' +
  'ZZiViXpyYYybin91Y3yejIN2aW6dkId4b2WVlYl7c2KIm4qAdWV5noyEd2tsm5CHeXBlkpaJfHRjhZuKgXZndp2NhXhta5mR' +
  'iHpyZY+WiX11ZYKbioJ3aXSbjYV4bmqXkYh7c2WMl4l+dmZ/moqCd2tymo2GeXBplJKIfHRmiZeJf3dofJqLg3hscJiOhnpy' +
  'aJGSiHx1Z4aXiYB3aXmZi4R4bm6XjoZ6c2iPk4h9dmiDl4mBeGt3mIuEeXBtlY+He3RpjJOIfndpgZeJgnhtdZeLhXpxbJOP' +
  'h3x1aYqUiH93an6XiYJ5bnOWjIV6c2yQkId8dmqHlIiAeGx8lomDeXBylIyFe3RsjpCHfXdqhJSIgHlteZWJg3pxcZOMhnt1' +
  'bIyQh354a4KUiIF5bniVioR6c3CRjYZ8dmyKkYeAe26AkYJ1cn2GnI+AcV1VnK+YiHVfbmSBrpyAhIVuSlWfrq6aemE9KaPd' +
  'spZ3VFhAc8u1i4eAZjtCpb20n3tgPiKY3rSXe1ZaQ2rGuI2IgWlAPp29tKB+YkQijNu3mH5ZXEZiwLqPiINrRjqVvbShgWRK' +
  'I4DYuZmBXF1KW7m8kYiFbks4jbu0ooRmTyV21LuZhF9eTlWyvpOIhm9QN4S6taOHaFMobM+9moZiX1JRqr+ViIhxVTd8t7Wj' +
  'iWpXLGLJv5uJZWBWTaLAmIiJc1k4dLW2pIxsWzBawsGcimhgWkuav5qIinReOmyxtqSOb141U7vCnoxsYF5Jkb+diIt2YTxl' +
  'rbakj3FgOk20w5+Nb2FhSYm9oIiMeGQ/X6i3pZFzYz9IrMShjnJhZEmBu6KIjXlnQ1mjt6WSdmVERKPDoo92YmdKeriliI57' +
  'akdVnramlHhmSUGbw6SPeWNpTHO1p4mOfWxLUZi2p5R7aE5Ak8GmkHxka09ssKmKjn9uT02StaeVfWlTP4u/qJB+ZWxSZ6yr' +
  'i46Bb1NLjLSoloBrVz+DvaqRgWZtVWKnrYyOgnBXSoayqZaCbGJJcrOok4hzaU9aorKYjnptWkyItp+RgnBlS2yvqpSJdWtT' +
  'V5yzmY58bl5MgbShkYRxZ01nqquUi3ZtVlSVs5qPfm9hTXuyo5GFc2lQYqWslYx4blpSj7Kbj39wZE51r6SSh3RrU16grZWM' +
  'em9dUYmxnI+BcWdQb6umkoh1bVZbmq6WjXxwYFGDsJ6Qg3JpUmqnp5KJd25ZWZWul419cWNRfa6fkIV0a1Rmo6iTinhvXVeP' +
  'rpiOf3JmUneroJCGdW1XYp6pk4t6cGBWia2ZjoFzaFRyqKKQh3ZuWV+ZqpSMfHFjVYSsm46CdGtVbaSjkIh3b1xdlKqVjH1y' +
  'ZVZ+qpyOhHRsWGmhpJGJeXFfW4+plox/c2hWeaidjoV2blpmnKWRinpyYlqKqZeNgHRqV3Slno+Gd3BcY5imkop8cmVZhaiY' +
  'jYJ0bFlwoqCPh3hxX2GTppOLfXNnWYCmmY2DdW5bbJ6hj4h5cmJfj6aTi390alp7pJqNhHZvXWmboY+JenNkXoqllIuAdWxb' +
  'd6KbjYV3cV9ml6KQiXxzZ12FpJWMgXVuXHOfnI2GeHJiZJOikYp9dGldgaOWjIN2b15vnJ2Oh3lzZGKOopGKfnVrXXyhl4yE' +
  'd3FgbJmejoh7dGZhiqKSioB2bV54n5mMhXhyYmmVn46IfHRpYIahk4qBdm9fdZ2ajIZ5c2Rnkp+PiX11a2CCoJSLgndwYXGa' +
  'moyGenRmZY6fkIl+dm1hfp+Vi4N4cmJul5uNh3t1aGSKn5CJf3ZuYXqdlouEeHNkbJScjYd8dWpjhp6RiYF3cGJ2m5eLhXl0' +
  'ZmqRnI2IfXZsY4KdkoqCeHJjc5mYi4Z6dWhojZyOiH53bmN/nJOKg3hzZXGWmIuGe3ZqZ4qcj4h/d3Bke5uUioN5dGZuk5mM' +
  'h3x2bGaGnI+JgHhxZXiZlYqEenVobJCZjId9d25mg5uQiYF4c2Z1l5WKhXt2amuNmo2HfndvZoCakYmCeXRnc5SWioZ7dmxq' +
  'ipmNiH94cWZ8mZKJg3l1aHCSl4qGfHdtaYeZjoiAeHJneZeTiYR6dmpuj5eLhn14b2iDmY6IgXl0aHeVk4mEe3ZrbYyXi4d+' +
  'eHBogJiPiIJ5dWl0k5SJhXx3bWyKl4yHf3hyaX2XkIiDenZqcpGUiYV8eG9rh5eMh4B5c2l7lZGIg3t3bHCOlYqGfXhwa4SW' +
  'jYeBeXRqeJSRiIR7d21vjJWKhn55cmuBlo6HgXp2a3aSkoiEfHhubomVi4Z/eXNrfpWOh4J6dmx0kJKIhX14cG2HlYuGgHl0' +
  'a3yUj4eDe3dtco6TiYV9eXFthJSMhoB6dWx5kpCHg3t4bnGLk4mFfnlzbYGUjIaBenZtd5GQh4R8eHBwiZOJhn96dG1/k42H' +
  'gnt3bnaPkYiEfXlxb4eTioZ/enVtfZKNh4J7eG90jZGIhX15cm+Ek4qGgHp2bnuRjoeDfHlwc4uRiIV+enRugpKLhoF7d255' +
  'j46Hg3x5cXKJkYmFf3p1boCRi4aBe3hvd46Ph4R9enJxh5GJhX96dm9+kYyGgnx4cHWMj4eEfXpzcISRiYWAe3dvfJCNhoN8' +
  'eXF0ipCHhH56dXCCkYqFgXt4cHqOjYaDfXpyc4iQiIR/e3ZwgJCKhYF8eHB4jY2Gg316c3KGkIiFf3t3cH6Pi4WCfHlxd4uO' +
  'hoR+enRyhI+IhYB7d3F8jouFgnx6cnWKjoeEfnt1coKPiYWAfHhxe42MhoN9enN1iI6HhH97dnGBj4mFgXx5cnmMjIaDfXp0' +
  'dIaOh4R/e3dyf46KhYF8enN4i4yGg357dXOEjoeEgHx4cn2NioWCfXpzd4mNhoN+e3Zzg46IhIB8eXJ8jIuFgn17dHaIjYaE' +
  'f3t3c4GNiISBfHlzeouLhYN9e3V1ho2GhH98eHN/jYmEgXx6dHmKi4WDfnt2dYSNh4SAfHlzfoyJhIJ9e3R4iYuFg358d3SD' +
  'jYeEgHx5dHyLiYSCfXt1d4eMhoN/fHh0gYyHhIF8enR7ioqFgn57dnaGjIaDf3x4dICMiISBfXp1eomKhYN+fHd2hIyGg4B8' +
  'eXR+i4iEgX17dXmIioWDfnx3dYOLhoSAfXp1fYqIhIJ9e3Z4h4uFf398eHWBi4eEgH16dXyKiYSCfnx3d4aLhYN/fHl1gIuH' +
  'hIF9e3Z7iYmEgn58d3eEi4WDgH16dX+Kh4SBfXt2eoiJhIJ+fHh2g4qGg4B9enZ9ioiEgX58d3mHioSDf3x5doKKhoOAfXt2' +
  'fImIhIJ+fHd4hYqFg399eXaAioaDgX17dnuIiISCfnx4eISKhYN/fXp2f4mHg4F9fHd6h4iEgn59eXeDioWDgH17dn6Jh4OB' +
  'fnx3eoaJhIJ/fXl3gomFg4B9e3d9iIeDgn58eHmFiYSCf316d4GJhoOBfXx3fIiHg4J+fXl5hImEg399e3d/iYaDgX58eHuH' +
  'iISCf315eIOJhYOAfXt3foiGg4F+fHh6hoiEgn99eniCiYWDgH17';

// at：点名「叮铃」上行三连音 E6→A6→C#7，约 0.32s
const AT_URI =
  'data:audio/wav;base64,' +
  'UklGRu0NAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YckNAACAgoKCgH58dXqLjYeEfHhta4qcj4l/dWtbe6ac' +
  'j4V0a1Nipa+WjXdrU0iRwKSUgGxcP3C8sJeJcWVGU6i7nJB4alNDisCllIJtYEJpt7GXi3NnS1Chu52Re2tYRIK9ppSEb2NF' +
  'Y7Gyl4x1aVBNmbuekX1tXEV6uaiUhnFmSV6rs5iNd2tUTJK5n5F/bmBHdLWplIhyaE1apbSYjnltWEuKuKCRgW9jSW2xqpSJ' +
  'dGpRV560mY97blxLg7WhkYNxZkxorKuUinZsVVSXs5qPfW9gTH2zopGFcmlPZKeslIt4bVlTkbKaj39wY053r6ORhnRrU2Ch' +
  'rZWMeW9dUoqxm4+BcWZQcaulkYh1bVZdm62VjXtwYFKEr5yPgnNpUmynpZGJd25aW5aslo19cWRTfqyej4R0a1Voo6aSinhw' +
  'XVmQrJeNf3JmVHmqn4+FdW1YZZ6nkop6cWFYiquYjYBzaVZ0pqCPhnZvW2KZp5KLe3JkWIWpmI2CdGtYcKOgj4d4cF5glKeT' +
  'i31zZ1mAp5mNg3VuWmyfoY+IeXJhX4+mlIt+dGlae6WajYR2b11pm6GPiXpzZF6KpZSLgHVsW3eim42Fd3FfZpeikIl8dGde' +
  'haSVi4F1bl1zn5yNhnhyYmWSopCKfXRpXoGilouCdnBfcJydjYd6c2VjjqGRin51bF99oJeLhHdxYW2YnY2Ie3RnYoqhkYqA' +
  'dm5geZ6Xi4R4c2NqlZ2OiHx1amKGn5KKgXdwYXWcmIuFeXRlaZGdjoh9dmxigp6TioJ3cWNymZmLhnp1aGeNnY6IfnZuY36d' +
  'k4qDeHNlcJaZjIZ7dmpmiZyPiX93cGR7m5SKhHl0Zm6TmYyHfHZsZoackImAeHJld5iVioR6dWhsj5mMh313bmaCmpCJgXhz' +
  'ZnWWlYqFe3Zqa4yZjId+eHBnf5mRiYJ5dGhylJaKhXx3bGqJmY2Hf3hyZ3yYkYmDenVpcZGWioZ9d25qhpiNh4B5c2h5lpKJ' +
  'hHt2a2+OloqGfXhwaoOXjoeBeXRpd5SSiYZ9eGtnhqGRiXptYXuPnYp6ZIaSdnODbYSvjn1gR2vIp5NxVz2WqaiFaj2PtoVy' +
  'dlN5y5qCWzpX1bKXb1Q0l66ph2s/h7aGc3VYc8mbhF4+Uc+zmHJYNZGtp4ptQ4C3iHR0XG3GnYZhQ0zJtZh1XDeKq6WMcEd6' +
  't4l1c2Bpw56HZEdIwreZd186hKqjjnFLdLaLdnJkZb+giGdMRbq4mXpiPX+poJFzT2+1jHhxaGK7oYlpUEOzuZl8ZUB5qJ6S' +
  'dVNqs455cGtftqOKbFRCq7uZf2dEdKeclHZXZrGQenBuXrGli25XQaO7mIFpR2+mmpV3W2KuknxvcF2spoxxW0KbvJiDa0tr' +
  'pJiWeV9gq5N9b3Jdp6eMc15Dk7yZhW1PZ6OXl3piXaiVfm9zXaKojHVhRIy8mYZvUmShlZd8Zlykl39vdF6cqY13Y1OArZKE' +
  'cVpprZaJc2RZpJ6NeGtTkaePfm9We6yShXJeZquXinVnWJ+fjXltVIynj4BxWHarkoZzYWOomIt2aViboI17b1aHp4+Bclty' +
  'qpKHdGRhpJiLd2tYlqCNfHBYg6ePgnNeb6iTiHVmYKGZi3htWZKhjX5xWn6mj4NzYWymk4h2aV+dmot5b1qNoY1/cl16pY+E' +
  'dGRpo5SJd2temZuLe3BbiaGNgHNfdqSQhXVmZ6GUiXhtXpabi3xyXYWhjYF0YnOjkIZ2aWWelYl5b16SnIt9c1+BoY2CdWRw' +
  'oZCHd2tkm5aJenBfjpyLfnRhfaCNg3Zmbp+Rh3dtY5iWintyYIqci391Y3qgjYR2aWydkYh4b2OUl4p8c2GGnIuAdWV3n42F' +
  'd2tqm5KIeXBjkZeKfXRjg5yLgXZndJ2OhXhtaZmSiHpyY46YiX51ZICci4J3aXKcjoZ4b2iWk4h7c2SLmIl/dmZ9m4uDd2tw' +
  'mo6GeXBnk5OIfHRlh5iJgHdoepuLhHhtbpiPh3pyZ5CUiH11ZoSYiYF3aXeai4R4b22Wj4d6c2eOlIh+dmeCmImBeGt1mYyF' +
  'eXBslJCIfHNijJ2Lemlrjp2CbH6Ce41tj5R7XmqfpodmOJ24lGw+h6Sjc0Cdmn58QbKnf1FEwbGIXB65vZFlLaOhnm45s5V3' +
  'eEPCnnlORdCpgVojxLWKYzOwl5ZrQL6QbnNMy5lzS0zXo3pWMMewg19AtpGOZk/BjWdrXMyVbUVa1590UULDrHxZU7SMh2Bi' +
  'vothYnDIk2k+bdKccEpZu6l4UmquiIFYebaJXViHv5FmN4LJmW5Ccq+ldUuBpoN9UZCth1pQnrWOZTKWvpVsPYikoHRGl51+' +
  'ek2kpINZSrKriWQwprWQbDybmZlyRaeWeHdOtJx+WEnApIRiM7Gtimo/qJGScEqykXJzU72XeFZOyJ5+YDy2qIRnSa6Mi21U' +
  't41sbV/AlHNSWMmbeVtKtaR/YleviIVoY7WMZ2ZvvZJvTWfGmHVWXa+he1xpq4VzWHGzkXJXdbGRclZ5sJBxVHyukHFTgK2P' +
  'cFKEq45wUYipjnBQi6iNcE+PpoxvTpKkjG9OlaOLb02ZoYpvTZugiW5Mnp6IbkyhnYduTKOchm5NppqFbU2omYRtTqmYg2xP' +
  'q5eCbFCsloFsUa6VgGtSrpV/alSvlH5qVbCTfWlXsJN8aFmwkntoXLCRemdesJF6ZmGwkXllY6+QeGRmrpB3Y2muj3dibK2P' +
  'dmFvrI91YHKrjnVfdamOdF54qI50XHunjXRbf6WNc1uCpIxzWoWjjHNZiKGLcliLoItyV46finJXkJ2JclaTnIlyVpabiHFW' +
  'mJmHcVWamIZxVZyXhnFWnpaFcVaglYRwVqGUg3BXo5OCcFikk4FvWaWSgW9appGAblunkX9uXKeQfm1eqJB9bV+oj31sYaiP' +
  'fGtjqI57a2WnjnpqZ6eOemlqp415aGymjXlnbqWNeGdxpIx4ZnOkjHdldqOMd2R4oot2Y3uhi3Zifp+LdmGAnop1YYOdinVg' +
  'hZyJdV+Im4l1X4qaiHVejJmIdF6PmId0XZGXh3Rdk5aGdF2VlYZ0XZaUhXRdmJOEc12akoRzXZuRg3NenJCCc16dkIJyX56P' +
  'gXJgn46AcmGgjn9xYqCNf3FjoY1+cGShjX1wZqGMfW9noYx8b2mhjHxua6CLe21toIt7bW6gi3pscJ+Lemtynop5a3Seinlq' +
  'd52KeGl5nIl4aHubiXhofZqJeGd/mYl3ZoGZiHdmg5iId2WFl4h3ZYeWh3dkiZWHd2SLlIZ2ZI2ThnZjj5KFdmOQkYV2Y5KR' +
  'hHZjk5CEdmOVj4N2Y5aOg3Vkl46CdWSYjYJ1ZZmNgXVlmYyAdGaajIB0Z5qLf3Rom4t/c2mbi35zapuKfnJrm4p9cmybin1x' +
  'bpuJfHFvm4l8cHGaiXtwcpqJe290mYl7b3aZiHpud5iIem15mIh6bXuXiHlsfZaHeWx+lYd5a4CVh3lrgpSHeWqEk4Z4aoWS' +
  'hnhph5KGeGmJkYV4aYqQhXhoi4+FeGiNj4R4aI6OhHhoj42DeGiQjYN3aJGMgndokoyCd2mTi4F3aZSLgXdqlYqBdmqVioB2' +
  'a5aJgHZslol/dmyWiX91bZaJfnVuloh+dW+WiH10cJaIfXRyloh9c3OWiHxzdJaHfHJ1lYd8cneVh3txeJSHe3F6lId7cHuT' +
  'hntwfZOGem9+koZ6b3+RhnpvgZGGem6CkIV6boSPhXpthY+Fem2GjoV6bYiOhHltiY2EeWyKjIR5bIuMg3lsjIuDeWyNi4N5' +
  'bI6Kgnlsj4qCeW2QiYF5bZCJgXhtkYmBeG6RiIB4bpKIgHhvkoh/eG+Sh393cJKHf3dxkod+d3KSh352c5KHfnZ0koZ9dnWS' +
  'hn11dpKGfXV3koZ9dXiRhnx0eZGGfHR6kIZ8c3uQhXxzfZCFe3N+j4V7cn+PhXtygI6Fe3GBjoV7cYONhHtxhIyEe3GFjIR7' +
  'cIaLhHtwh4uDe3CIioN6cImKg3pwiomDenCKiYJ6cIuJgnpwjIiCenCNiIF6cI2IgXpwjoeBenGOh4B6cY6HgHlyj4aAeXKP' +
  'hn95c4+Gf3lzj4Z/eHSPhn54dY+Gfnh1j4V+eHaPhX53d4+FfXd4j4V9d3mOhX12eo6FfXZ7joV9dnyNhXx1fY2EfHV+jYR8' +
  'dX+MhHx0gIyEfHSBi4R8dIKLhHx0g4qDfHOEioN8c4SKg3xzhYmDfHOGiYN7c4eIgntziIiCe3OIiIJ7c4mHgntzioeBe3OK' +
  'h4F7c4uGgXtzi4aBe3OLhoB7dIyGgHt0jIWAenSMhYB6dYyFf3p1jYV/enaNhX96do2Ff3l3jYR+eXiMhH55eIyEfnl5jIR+' +
  'eHqMhH54eoyEfXh7i4R9eHyLhH13fYuEfXd+i4R9d3+Kg313f4qDfXaAioN9doGJg312gomDfXaDiIN8dYOIg3x1hIiCfHWF' +
  'h4J8dYWHgnx1hoeCfHWHhoJ8dYeGgnx1iIaBfHWIhoF8dYmFgXx1iYWBfHWJhYB8doqFgHx2ioWAe3aKhIB7d4qEgHt3ioR/' +
  'e3eKhH97eIqEf3t4ioR/e3mKhH96eYqEfnp6ioN+enuKg356e4qDfnl8ioN+eXyJg355fYmDfnl+iYN+eX6Jg314f4iDfXiA' +
  'iIN9eICIg314gYeCfXiCh4J9d4KHgn13g4eCfXeEhoJ9d4SGgn13hYaCfXeF';

// banner：事件横幅双音叮（E6→A6）——settings.js 的原 DING 原样迁入
const BANNER_URI =
  'data:audio/wav;base64,' +
  'UklGRg8NAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YesMAACAgIGBgH17e36C' +
  'hoaDfHd2eoKKjIh+dXB0fouRj4N1bG14iZWWi3lqZnCDlZyTf2thZ3uSoJyIb15fcYyhpJN3' +
  'X1hmg56qnoJjU1t3mKypj2xTUmmOq7GceFZLXICltqqGXkhPcZu3tZZrSkVhjbO9pnpQP1F8' +
  'qsG1jFw+RGqcv8Cea0I6WIu3xq5+TjpKeKnEupFePkFmmL3BoXBHPVaGssKvglQ9SnSjv7mU' +
  'ZENDY5O4vqN1TEBVgay/r4ZZQUtxnru4lmlIRGGOs7yleVJDVX2nvK+KX0VLbpm3t5ltTEZg' +
  'iq65pn1WRlR6orivjWNJTGuVs7WbclFJXoaqt6aBW0lUd560r49oTU5pkK6znHZVS12CpbSn' +
  'hF9MVHSZsa6SbFFPZ4yqsZ16WU1df6Gxp4dkT1Vxla2tlHBVUWaJp6+efV1QXHydrqaKaFJW' +
  'b5Kqq5V0WFJlhaOtn4BhUlx5maumjGtWV22Op6qXd1xUZIKfq5+DZFVcd5appY5vWVhsi6Op' +
  'mHpfVmN/nKmfhWhXXXSSpqWQclxZa4igp5l9Y1hjfZimn4drWl1zj6OkkXVfWmqFnaWZf2Za' +
  'Y3uVpJ+Jbl1ecYygo5J4YlxpgpqjmYJpXGN5kqKfi3FfX3CJnaGTemVdaICXopqEbF5jd4+f' +
  'nox0YmBvh5uglH1nX2h+lKCahW5gY3WNnZ2OdmRhboSYn5R/amFofJKemYdxY2R0ipucj3ln' +
  'Ym2Clp2VgWxiaHqPnJmJc2Vlc4iYm497aWNtgJOclYNvZGh5jZqZinZnZXKGlpqQfWtlbH+R' +
  'mpWEcWZoeIuYmIt4aWZxhJSZkH9tZmx9j5mVhXNnaXaJlpeMemtncYKSmJGAb2dsfI2XlId1' +
  'aWl1h5SWjHttaHCBkJeRgnFpbHqLlZSId2tqdYWSlo19bmlwf46WkYNzam15iZSUiHlta3SD' +
  'kZWNfnBqcH6MlJGEdWxteIeSk4l6bmt0go+UjYBya3B9i5ORhXdtbXeGkZKKfHBsc4GNk46B' +
  'dG1we4mSkIZ4bm53hI+Sin1xbXN/jJKOgnVucHuIkJCHenBudoOOkYt+c25zfoqRjoN3b3B6' +
  'ho+Qh3txb3aCjZCLf3Rvc32JkI2EeHBxeYWOj4h8cnB1gYuPi4B1cHN8iI+NhXlxcXmEjY6I' +
  'fXRwdYCKj4uBd3FzfIaOjYV6cnF4g4yOiX51cXV/iY6Lgnhyc3uFjY2GfHNyeIKKjYl/dnJ1' +
  'foiNi4N5c3N6hIuMhn11cneBiY2JgHdzdX2GjIuEenR0eoOLjId9dnN3gIiMiYF4c3V8hYuL' +
  'hHt1dHqCiouHfnd0eH+FiIiFf3dxdICOlIt4amt8jpSKeXF3goiCeHeCkJB/aWR2lKOWdVxf' +
  'fZyhimxjdY6Xhm5ne5igiGRUa5exonRPU32nrYxiVnGYpYxlWHSer5JgR2CYva51RUh7sbmP' +
  'WUltoLKSX0trorydXztUl8i6dzw8d7jFk1I9Z6a/mls+YaTHp18yS5TNwXw6NnO5yZdTO2Ok' +
  'v51eP1+gxKlkNkqPyMGAPzdvs8aaWD5hn72fYkJdm8GqaTpJisPBhUQ5bK7EnF1BYJu6oGZF' +
  'XJe+q24+SYW+wIlJO2mpwZ5hQ16Xt6FqSFuTu6xyQkmBur+MTj1mpL+gZkZdk7Wibkpaj7et' +
  'dkZJfbW+j1M/ZKC8oWpJXJCyo3JNWYu0rXpKSXqxvJJYQmKbuaJtTFyMr6R1UFmIsa1+Tkp3' +
  'rLuVXERhl7ajcU9biaykeFNZhK6tgVJLdKi5l2FHX5O0pHRRW4aqpHtWWYGqrIRWTHGkt5ll' +
  'SV6QsaR3VFuDp6R9WVl/p6yHWU1voLWaaUxdjK6keldbgaSkgFtZfKSriV1PbZyznGxPXYmr' +
  'pH1aW3+io4JeWnqhqotgUGuZsJ1wUV2GqKR/XVx8n6OEYVp4nqmNZFJpla6ec1Rcg6Wkgl9c' +
  'e5yihmNbdpuoj2dUaJKsnnZXXIGjo4RiXXmaoYhmXHSYppBqVmePqZ95WV1/oKOGZF53mKCJ' +
  'aF1zlqWSbVdmjKefe1xdfJ2ih2dfdpijjWpbbpCjlXNcZoehnHxgYX6coIZmXnWWoo5tXW2O' +
  'opZ1XmaFn5x+YmF8mp+HaF90k6GPb15sjKCWeGBmg52bgGRiepifiWpgcpGfkHJga4mel3pi' +
  'ZoGbm4JnYnmWnoptYXGPnpF0YWuHnJd8ZGZ/mZuEaWN4k52Lb2JwjZ2SdmNrhZuXfWZmfpea' +
  'hWtkdpGcjHFjcIubknhlaoSZl39oZ3yVmoZtZXWPm41zZG+JmpJ6ZmqCl5eBamd7k5mIb2V0' +
  'jZqOdWZvh5iTe2hqgJWWgmxnepGYiXFmc4yYjnZnboWXk31pan+UloNtaHmPl4lyZ3OKl494' +
  'aG6ElZN+a2t+kpaEb2l4jpeKdGhyiJaPempugpSTgG1rfJGVhnFpd4yWi3ZpcoeVj3trboGT' +
  'k4Fua3uPlIZyanaLlYt3a3GFlI98bG6AkZKCb2x6jpSHdGt1iZSMeGxxhJOQfm5uf5CSg3Fs' +
  'eYyTiHVsdYiTjHptcYORj39vbn6PkoRybXmLkol2bXSGkox7bnGBkI+AcG99jZGFdG14iZKJ' +
  'eG50hZGNfG9xgI+PgXFvfIyRhnVud4iRinludISQjX1wcX+Oj4Jzb3uLkIZ2b3eHkIp6b3SD' +
  'j41+cXF+jY+DdHB6ipCHd293ho+Ke3Bzgo6Nf3JxfoyOg3VweoiPh3hwdoWOinxxc4GNjYBz' +
  'cn2KjoR2cXmHjoh5cXaEjop9cnOAjIyBdHJ8iY6Fd3F5ho6IenJ2g42KfnN0f4uMgnVyfIiN' +
  'hXhyeIWNiHtydoKMin90dH6KjIJ2c3uIjYZ5cniEjIh8c3aBi4qAdXR+iYyDd3N6h4yGenN4' +
  'hIyIfXR1gIqKgHZ0fYiLg3hzeoaMhnt0d4OLiX51doCKioF2dH2Hi4R5dHqFi4d8dHeCiol/' +
  'dXZ/iYqCd3V8h4uEenR5hIqHfHV3gYqJf3Z2foiKgnh1fIaKhXp1eYOKh311d4GJiYB3dn6H' +
  'ioN5dXuFioV7dXmDiYd+dneAiIiAeHZ9h4mDeXZ7hImFfHZ5gomHfnd3f4iIgXh2fYaJg3p2' +
  'e4SJhXx2eYGIh393d3+HiIF5d3yFiYR7dnqDiIZ9d3mBiId/eHd+hoiCend8hYiEe3d6goiG' +
  'fnd5gIeHgHl4foaIgnp3fISIhHx3eoKIhn54eYCHh4B5eH6Fh4J7d3yDiIR9eHqBh4Z/eHl/' +
  'hoeBenh9hYeDe3h7g4eEfXh6gYeGf3l5f4WHgXp4fYSHg3x4e4KHhX55eoCGhoB5eX6FhoF7' +
  'eH2Eh4N8eHuChoV+eXqAhoaAenl+hIaCe3l8g4aDfXl7gYaFf3l6gIWGgHp5foSGgnx5fIOG' +
  'hH15e4GGhX96en+FhYF7eX6EhoJ8eXyChoR+eXuAhYV/enp/hIWBe3l9g4aCfXl8goWEfnp7' +
  'gIWFgHt6f4SFgXx6fYOFg316fIGFhH56e4CEhYB7en6DhYF8en2ChYN9enyBhYR/e3t/hISA' +
  'e3p+g4WCfHp9goWDfnp8gYSEf3t7f4SEgXx6foOFgn16fYKFg357fICEhH97e3+DhIF8e36C' +
  'hIJ9e3yBhIN+e3yAhISAfHt/g4SBfHt9goSCfnt8gYSDf3t8gIOEgHx7foOEgX17fYKEgn57' +
  'fIGEg397fH+DhIB8e36ChIF9e32BhIJ+e3yAhIN/fHx/g4SAfXt+goSBfXt9gYSCfnx8gIOD' +
  'gHx8f4ODgX17foKEgn57fYGDgn98fICDg4B8fH+Cg4F9fH6Bg4J+fH2Bg4J/fHyAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA' +
  'gICA';

const SCENE_CRUNCH_URI =
  'data:audio/wav;base64,UklGRp0JAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YX' +
  'kJAACAf4GBg4CAfXp5dIZ7fYiQfoCKi4x0dmiYe4Vse36PdXSMeGxkaYmGZpKPfIt/gI53kW9/e3' +
  'ZplXSVfoGTkISHcJWbeYJmdIppg5OOeY+Ckpl5b32YZXiacYV+cXOUZnN2bHOKjmyDh4t8eJJ4hY' +
  'pweYKEfneHjGl7anB+dH9sfXJ+iW+RdpZ7j5BleoJmd2qSkneEfJqbe2htc3mKaWZrmWR9h5Joe4' +
  'SPlJOUa4loiIuEkIRtjXpvmoGFgo5/mGuGl2yWkXFla32ZfYqEaHuCh42MeYR7kop8kpKGaZZkgI' +
  '1pe49rdnWEkXl3kZJ7ZWZnfGaUeo9nd5mDenuUd5SKdn56ZpGDhIeKbnVujYFuZn6OcZCWhntokI' +
  'uUfpB1ioKMjXSUmX13bIRmfXGAmXZqh4KDh5lvdZF0c4yOeW6Wf3tykppwl4N9h4lofWqEmXaEc3' +
  '2LlXqUhZeEbmZ9eItlcnSKimyOaXSFeZmZZ4VvkWWUhmuZkIxwgnCKeoablZdlhIlphndsdnVomX' +
  'R2fnp8jJFsjZl5h3xsi4Oah3CIe3GSjIh7dYyWfpCRgJdxZHyampN/dGmOcohoin+JlYxscImYlp' +
  'STc26YbGtohYZ2hmmOiYGJdZOIiGiMen2KkYRwj42IfXFlZHSVlYR4dWyFjJBugoJpZ3+OkpWSiX' +
  'qIjnxsf4Fre4Nlc4NxaX+Cb3l9iI5ul4WDeIiFe3J9ZIeDc3plmH54k41umHhogIRwaWuQb4GGap' +
  'aKkm9xeW99jIpnaYt4h3FxjnNwhmmNfoORaoeGhHyNg2yDa3CUeW92iYN2g49tc4R8eHNxcH+Mj3' +
  'F7i4aCf3Vub3eCjnZ+do5ycHp5iYpzhIOMc4V7jH6MiHmLi3OIiniDh3uDf4B4dHiLhn10gol3fX' +
  'p7h4iGf3x9hYV4dol5gIB/dnl8gXqBf4d3fH1+fHh4f4CGeoSBe4KDfIB7gX96e4SDhH+De4KBgn' +
  '9+fX2Bf4KBfYB9fYCAfoKAgYB+f3+AgH9/f4B/gIB/f4B/gICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAf35/gn+Cf3qFhod6ioaDhnGBcXeKkHtrj3B3gZOReo6AdY+Aa5Fwgo' +
  'htg3t1bIlxf4h7cHmLfX6QdnWEh3aGbnx0dYR7iHqCjoZwhX2UkIaCd4yTg4J4fX2CcIZuk4KBj4' +
  'aOkXOHkIFvb4tveXGBhXp5bX2TjnJ0c4OMb4eNcY10iIZugHV9dnB4iW6HhoZzhXuFi4h9hol4cI' +
  'Z3kX6LjIB7f5NsbZB2dZR+g5GThm9ygnSCc3xxlI1zhHSNj454fH2Pcntvc4KKd5CQgopshnSMe3' +
  'mKfJOTlIF0cm6DgnaOj42GbpGPcoWSd4p5e3Fxcn+CgIGBcHaLjYZ6kHuBdoZ4f4N5fHOKkYGIcH' +
  'FzbGuLa4uJk4SOjoGDb3hxeIB2gI+LjYB9bnZubXuQc5SCjniEd2t/fHx+i3KLfIZ1bZORfYWCdI' +
  'RucnF6hY50bI6PbZJshH9zcINvdJJvgnhxeYeTdX5xlIOBinx1hXt9iIiGiYCRdohth4pvdYxyfY' +
  'KTfoqHfG5rk4V5g49zjoyPcoiGf3eCcoKBjYlykpKBgXaTcHt0dIR9enZ7j4eBc5J6jI+IeoiEkH' +
  'iNk4p8gZSLhXt3gX+NjoODcoqObIqPbH6AlJN1hpR/jm2AbH5zkX6Bb3Rzjm6Ra4V4g415eoF2bY' +
  'V6eY14hnFza2xvjm+HbnNtlIiCeYSPkX+CgoxzkXB1fGt3cIh0cJJ8gYGLlH2Th3WIbHCTbHhwjn' +
  '+LdG+DeH2QgX1/dn97bnB9jXd7hXh6jn13iHmFdHuBfYGQhYVyhYeLeomAjIhwgnx7jodxdoaBgX' +
  'V+d4l1jHN2eHeJg4t9g3d4jH2JgHWCeXqDdoWHdH93gHmAfHd9fH+AfHeAdYJ8hISBfoZ7iXZ5fI' +
  'KJeIGBd4J9enuFg3uHhoaGg3l+eHyHg4R9gYGFfoKAhIJ6hIR+f4N+gIF8foCFgYSDgn6Dgn6Df3' +
  'x8goJ8gX+DfX1+fX9/fH5+gIF/gn+AgX2Bf4F+gYF/foB+f4CAf3+Af4CAgH9/gICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA';
const SCENE_SWEEP_URI =
  'data:audio/wav;base64,UklGRtMXAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0Ya' +
  '8XAACAf4CAf39/gH+AgICAgH9/gICAgICAgH9/gICAf3+Af4CAf4B/gIB/gH+Af3+AgH+Af39/f3' +
  '+Af4GAgH+BgH9/f4CAf4B/gX+AgH9/gH+Afn+BfYKBfn9/f4CBgX+CgYGBf4CAgIB+f35+gICBf3' +
  '9/gH2CfoCAgoB/gH+Cf3x/f399gX+AgYB9gn2Agn+Af4CBf3+ChIKCgH9/gX+Ef4N+goB/gIJ/gX' +
  '6AfH+AgH6BgoR+foB/fn9+g4R9gH6BgIB/fX2EfYF/fH+Af4F8gH56goCAf359f4J8fH2Af4GAfH' +
  '2Df36Bf4B8foF+hX9/gYF9hYV/fICAf36Cf355gYF/f4KCf4SDgoF/fX+Ff3iDfIB+gIJ6g3t9gX' +
  '+Bg4V8gIR/gIN/gHp+fn+Af4GBgH6Fgn+BhHp4g4OCfoGFhoCCh4N/fYOCe36Fg4CChYN/enuAf3' +
  '59hHuGgX+BhXuBfHmEgIF/hYqDeIF/fIZ7goWCfn59g4Z+hIN/fnmJh399f32Bg3iDhH6AhX15iH' +
  '98gYF9iHyHfn96g4WFgHyAgnd+fH9/gISGh3SDgX9/f3mEiH9/gId2dYl+f4J7h4iDfoCDhH56hn' +
  '99f4N3fXp+e4SJhomDgHyAhnN+fH6Ad42BfICEiHx/d4N+f4qNhX5/gIl9hIN9fn1/gXx+gHyDdn' +
  'Z+fH59eH97fIB6h3J2eIB6eIaMeXx9foF5fnp/fXp4gYV/fYJ4dH1/fY1zdoZ+enqHeol/gYh8c3' +
  'aAfIdxhXuBeohvdoZ9eoWEc3iAhH1vi4Z/g3WQcoiAh4qIenp/d3qJjHuAeYF0gYF8eoSGg3+BgX' +
  'R+eH95eI+Hg3+Ajn97fIB9c4qAgHyEi3+CgoCGjoN/f41wfn+BfHN1hH6EeYlxgICGiYeLfIJ/dY' +
  'l9f36FjIl/fHOIeISBhY13hoCCg31/gH6DhIh8f4WAgIB9gXh4e4ODg3N5gH97fn99gId6hXWBg3' +
  '2KdH99h4mIfn+GcYOHgIGKg4WBeI6Fg36HjXCCfoWDf4GAhH6HgYB9iG+Fg4B3gnh/gXRwinx+d4' +
  'WFfoCIdYaAgYaHfYF+g3iBfn6CenN6g4qKfIR+jISFhIB6jXaChoOPhIJ4fImIgXyFinl/e4t9fo' +
  'F9joCAg3R6c398d4R2fnyKh4V/gHWJhYCLjI2De3qCdICAhX14f3qHgoB/jIOGfoeFcIOAfomHgY' +
  'OAiH6Ahn9/en6Lc399gHWHgYCId4aDg3iDgH56cot8gYaBgnx/g4OAf4J5dH18hoGFgIhzgXt9g3' +
  'J7f3h3g3qAfYuAgHmBiXuFhYmEf32IgnyDiHp8foh7gX98gnKEhIN2gn9+hoGAgIJ6iH5+hYl+hI' +
  'h7goOJdICBent8fneDgoJ6hoyEgISNdn+DhX+AeXiCeXx1h4CAfn2Jf4aKd32FhHGGg4KLhH93hI' +
  't/eY13gIR/fYKAgIWDf4GMeYCFgIZ/fIeMgIWCioKAjHR5gYeKf352hXt/fnZ3f4WEeX6BjnR+eI' +
  'J9gIWAint9inZ+fYCPfoF0eIV/hX6EgId2e4B/iXqAhop2foN9hoGEcniBgXF0fniCdoB8iIt8hY' +
  '15fneEiX6Dj3V9eY6NgIaBcn92gYGAf4R/f3uMdIGEd3d/eXp3f3yIiH+CfHSAdXp3gHmFeX6IfY' +
  'B/couAfHiNf4F1goN9eIaDeYqAgYN8e4B6d3d/h3B4f4l8f4N/hXuDdHp6fH19gXmHfn+GfISAhX' +
  'p3f4yEg4KGe36FiYd9h4d5f36Oen+GdXyEdouAhnqIgX12e39+c4SCiHiDgIODgIWIfYGBiYOBh3' +
  'GHgXaJf3t0foB1ioSBeIGGgnOKgYV0f39/iniBg3qChop3f3qMhH+Cc319coSCiIJ2f31/en5+hY' +
  'N6c3d/hnl4f3Z5hXl/in95iYKCeY1/eYOMgIJyhH+IgoJ/jn1/iHl6g4iLfIOJiH98h398iX9+hY' +
  '6Ff394fHtwdH97fX99e4d7e4x0gH+HgIR/fX6JgneBeYqBfXuKf3N/e35+g3+DfHp9h39/e4KFfn' +
  '51e3eHeX93d3+Fcn5/h4WEf31yf4qAeXx0gYF5gHd9eHV7f3OIf4F3f310dYCJhIaBdHp/fo+DhX' +
  'B5gHeBhXx2gn94eYWDhoN/h3qAfoh5gXmJeoOBfX6AeniAiXuCg455g4B6f4RzhIN2gn51i4N/jI' +
  'F/dHB8fHqFgH2JgIGBc4CAdIZ6e3aAjIJ9fHl2gHmOhHeHh4J4hH96joJ7f4B/hYh+f3B9gIp7e4' +
  'F0eX+IdHuAgIJ/iYF+gnaEfIqBfoKMfoKEfICKhYCAg3t9eXt9fn2HhI2MgYp0eXp3ioCJdH+Hio' +
  'd9hoSBdXuCg49zf3R7hHpyfX2FhoJ9coaBcoqAhY2EfXyCgIZ0fXVye4R1hICLh4B4eHh/comAfI' +
  'aFg4CJgId5gn59hHyGdn56cHmGeYKBjHuBg4mIfYN7f3iHfIV/dn6Gh4F6iHuBcYmAeI58gHh4gY' +
  'aJf3WAgoiDfnxxc4B5jnl9eIeCfIR9dm+He4uFgYWHfoKDgX90gn+KcX6IcHp9cIuAdI58fH6Gfn' +
  '1+gIqPhIGOiICLd4KIcYWAfoB/d4V+eoV9foZ2gIR8gn9+iISHeH94fX12eXd8fHV/h3qEgoSGgn' +
  'V5f3mPe4GCgX52hn+Mjn6HjXt9hHSAhXqEeY58g32KgHZ9f3t1hn6AhYCCdHmEgYR/doN/enV8go' +
  'GFgHyNf3R/f4SGhYKNgn95goGFhnqCeox+f4p/fHeJf3F1gIeIf3iHiH53fYCKdX96gop/gnWAhH' +
  'N5e4CLfneNgH14hYSHdH+Ejn94iXqAfnR+jHqBeoeFgoWJgYSKgIeNgXx1g4B0gn+CdHh/cYSAeX' +
  'eAgn+Ieod5gHV+gn19d4SEhoCHd4CDcHZ9c3x/eIR8g3V3fXaAgYF0fnuChH6Ajn52iH2FiYWAd4' +
  'V/gn9/eXd/f398gHV+fnqOiICNhIF4d4l8eHl/gnyBfnCCfHGAgXh5hIN0h396fn2Kd4GAi4CAhI' +
  '1+iH13gH6Cf3l5eYaDiIJ0cn92gYSEfn6Ah4h/eouGf46If4iDgYBxeoJ0coB2iISEhnyAd3h7eo' +
  'yEfHpzgIeEeoV4dYGFjH+Gh3h+jYWAen5/fH2Df4N4hIKQhX57hn94cH98hHl+iYF8en52goF5f4' +
  'p5fYB2g394dIR9e4l9coKBiXd6fnSIgXp1e3+PdH9ziYJ5hnl+eHJ8hH6GgXqKgIN/gH6Je4B7ho' +
  'KHc4h/d4GChYaJgXeMf3d3goCFe35+fXx7iIl/e4mBgIF1gH97e3mIiH+FdYZ7i4GAhXd6hYx+gH' +
  'p8gId/h4F3cn+Ejnp+fYCBeXyFfYN1hHh3e3+MeX54hnWAiX+EiIKJgIyFg4F6fH+IiIF8dXZ/gI' +
  'd5goGJgHZ2f4GEiX6Jc397d4eBgG9+gYd7gYKJhIB/h32Ef3qAioB+fXt5f4R5gIONeIJ/dIR/hX' +
  'V+enmEf4OFhnqFg4GJhX2AfY6EgId/f4iNhX+FfX59jYN/iHF6eXGCgIKBfX+Cc4OCc3t/dnqEfH' +
  'R3f4N6ioGDcISBhXqAdnuIgnl+gIaPfX98iYGAho57hH1zgYJ+fIB1ioZ/eXKBiId9gHt3gHyKfY' +
  'KFh36AgXV7f42NfHpyfoGAjnx/gY5+fXV7eniFiX+HhYJ+en54gYFxhIB5gX2EcH5/eIx7gHGBho' +
  'V5c354fXp/e3GFgH94fIFyeXl7fop8fnSBgYl8fYCHgnt/enV+gIJ9hISMcn+GfoSBfIh8gH96iX' +
  '+Hf3+AhHyEf32LhoN6d3yGjoqDgoJ5foB+fH2GfYCAeIGCgH+OhIB0iYCAjIx9f3tygYGKjISAgI' +
  'x+fneNeH12d4GCg4B8fnl1g4GHcoR/fnN8fnd3h31zeoh/eoOCf3uAhoGAcX5/dYGDf4d/fICCeX' +
  '1+eYSKgnt2hYOBjHGCfnZ2fH6DdYeBd317gYuHfYCKdnh/eISLfoGPgX96iXqGgoZ/gIB2fYJ/eZ' +
  'CLgoGLf4J8f3p7gIV4g4CCe3qAe457g3uLjX9/h3iEgIVzh4GFc4qDgIR9hn+IeYKAfnd1foV7en' +
  't+dI13gYVzhXqDgnCDf3Vxin6BdIaFg4qMgn99eYyEgYCIeIB1g4qAf4uEe4CEgYh+fXRxfH90i3' +
  'R+eX6Kh395cH1/hHSNfoCHjXqCfnR9gn97jHR+f3qFg3+CdYl7f4KBdYKDfouJgXh+coOBe3KHgI' +
  'GBhn6AhHxxgn+IfnyBgIONgn98dHODgIFxiIaAiIR2fICBfXqDhXKQdn2Afoh5gIF+goaAfoCOfo' +
  'B7fo2EgHmNeIODeYqGfn6LeYGAgIeAcoJ+gHKFf4KJioWCe3h0ioCDgY2MeoJ4iYt7gHWFhXyAgX' +
  'OEhn+Ed4J2gH+Of4GBh4l4fn17jIt/fYB8fnqGf4eKdnaBhod0g36Dg4N+eX57cXp/gIOGhIl8g3' +
  'mQg3t/f3h0hYB/inGHf4J6dnmFf4V+jHl9fnhziIGCgYKHhoaBhoeFioF/fYd5eX+HjXp2foB6dH' +
  'h3foJ/e42JgIOFenuBf3eAfHaBgX1yb3d/g4V5gIZ+gYN/dIV+gn99doSBgHiHgnyAe3uMhIWAgn' +
  'eBg4WCfnuLd3WAe4OGiHV8gXl8in2CgH99cYeBgIZ4hXV4goGFh356f4J+e4uDfYB/dHGOiXyAh3' +
  'R4gnp/fox3in2Cf4R3eX2DgIR/iXd2fYCEfHN0g359dX6AdXeAe3VzgIeHgICKcnKJgH+Af4SKhI' +
  'Z/hHZygoB/gIZ1eIqEgH99iH2MeYKBfIJyeYB8g4J7cn9xiH6AgIl1cYSHgIR1i3J2dnt/enV+i4' +
  'x+gX6Dg3t1foJ/f4mCi4OEg394dYyBeoOEgHxzcH57fn9+f4l2cYOAgIF9fXKIgXuCf3p5fICKh4' +
  'R/gIV5dnpyg3yAe3+Pc3d4fX+Bf3mMfnh9e3+Cg4R9hoiCf396hIaEdXeGf397gXmEh3R3fX9+gX' +
  'OEgHKBhIGBeoyMe3+Fh39/gnl6fn2HdXh+gYKAg3qMfXt/gH95dYR8e4KEgH9/fXt/coeIdoWDgH' +
  'yFd3t6e4WEf4GBgHV0fX6GdYmFfn95in9wfoZzd3p/gIGFiYx8jH5/g3+AfYd/h4t8dHN7gX5/e3' +
  '2DeICMdIeGfH+Afn2CdXZ3i3mDgnqAgH2DeYF5enKHg4mCg3+Dhn6Ignd0fHuHhYOAgIR9eHqLh3' +
  'J5iYp6fIN/gX18eH55d4iCdHSAeoOAfoV3iHaDfH19dnuCgXt/gH1+fYWAhnSKhnSOh4CIeoOAgn' +
  '6DeHqAiIl9hoh6i4R3hYF/f4N7gX2AfHR3c3B5hXOHg4GCfYCAfHuCd4tyiop4dHhzcn14d4CCe4' +
  'J/f32Dg36Fe4WKgXyKgItwi3qDfHeIfoN/fn9+fYWFeIN+doSDgXaNc3OKdn5+hIJ2dnl5gH+CgH' +
  '+Af4CBg3iEeXWEfoBzjnN3fHhxdI6EdHl2jYCHfXZ9hoaEhYF8e4F+f4CAfn6Ce4R6eX6Hh4J3ho' +
  'F/fnSCeouJfXl3hnaNe3WLh4GIjnF6eYyJe3WLd4OCjYaNhHd/eoeHgX6FdHqFh4yCdXaIfIWGiI' +
  'OLgnp9dYd+fYmEenmGhnt6eIiMgoaNi4qCgnaHfHR6g3R4jYmIhYKEdI9yg4SBi4B0eomFioiGcI' +
  'x+enqEdnyAenuHhYV2hIOFgoCEe4KBgYCAf4CBgnt/hnuIgn+CfXuIfHZ+eHVvhY91inCGdXeKhY' +
  'F5hYCFgn6AgH9+f399fIaGh3R0dneEg3dygo98hoF3hYiBf36CgIB9e4R5gYdzeXaFjHeHeIt/f4' +
  'uJhHt7gYB/fnyBh4h2hXN1cXOGgnqCenp+fX5/foKGd3mEi3F1inCNdXyEg3x/f4F8hn11fHSFf3' +
  'J5iHp5d4J+gIKChHaHe3R9jnWFgHuEgoGAf3qGen2AfnR3dod6fX6AfYWIeoGDg498e4F7gICBgY' +
  'SIfYh+g3iFiIKDgICDh3+GgnuHe4uBhYB+gX19h4WGi4x9gnt/foR6jXh2dnd1eX1/gIN+e49zfY' +
  'h7eYKAg4OFgnGEd313f4B9hHxxg4p7dHt7f4KFdYhwhXWCeoF/foF2hpB2eXt9f4B5ioR7ineAfY' +
  'B9dnKMeIaGhnx/hX6GfH52d4R+f4SEhXR/gnh/gIB7eXuLjHmDgIGCioZ1gXeAgXl3iXt0eYJ+gX' +
  'eJiH2HfX9/gIZyf3GGgoF/gYFxjH99foCFdYGNf4WBf4SJgI2Afn9/hHN3dYl9gIF3iXF4h3p+f3' +
  '+JjIuJgYCBi3CDiXl9goZ2d4N7gYCEeHaIioKAhXqHfXSFf3uCgICHg3+Dd3yGg3+AgHyChniIgH' +
  'p6jHZzg39+dHKOf3uBeYN8fXd9g4F+hIuGgIWJeH93fIB3coeIfoB/d398h4GDdo52dH6Ae3dyg4' +
  'Z/h4t8foN/eYN+enx/fnqOeYF/gHmAend/enSAc36Ae4B0dHt/hIWDeoV/fX5yeHt+fXiIin99in' +
  'J1hH+Ff3R3hH6If4OIfX2IeXt6gH6IhoZ/f4F4h4R/gYmFen57iIV4gX6FhYJ+f3+JjImBgYuAio' +
  'SCgY14fICHgoGAf3uMeoGBgoaDhoB8eXiIfHyBdoZ/fXp7ioOCiX91gH53enx9gHyGfH6BhoZ3gI' +
  'OId4KAh4aIfYCHdoR9fnh+eIF/gnp/gX2LfIJ/hX6HfoF+h3yBgn9/goCDhYh8god2foCDiXmCgY' +
  'F/iH6Ad3t9fXeDhoCFh4GAgoB8hIB+gIB8gXqLeX9/gnt+gYaLfX2GiX5/hH17gHt7eIJ+goN7f3' +
  '59e4F8fnl+f3uHf36EgoN+fICAgHiCgICGhYR9gHyDfXx3hoSCeYF+goJ/e4GBf4J+e316f4F8hY' +
  'GAhn9/gYGCgXp/fn6Hf4GAgYqCf4WDhX98eoR+h3eAgH56goJ/fn9+iIJ/g4V5gH6Ge354eoOBh4' +
  'CCe4l/gHyFfYCFhHuBh4GCe4N/fn55hH+CgYCBhXp+e36CgIR3fX6IiIF6fH6Ah4GDfn6GgYGFg4' +
  'F7eX+AeX+AfoF7gnp5gICEgIF5goB9fnuAhoiBfYiFf4Z/f4F4fX+ChIGDgIGAh4R/g4B/fn96f3' +
  '+HgYKDf4CDhX6BgICBe3t/hYN/f3l8f4J6f357g31+eoB7hoGEf4OAgXp/fXt/e3t+fn+BgH6Ef3' +
  'qCgn+Bg4F4gn9+gYCFg3+CfIF+hoSAhoSAg3p/e3uDgXl8gYODgICFf4CGf4WDgX+Eg39/hH6BfH' +
  '+Ge3+Egn9+gX6Af4GDeIOBgX5/f4N/foN/gIF/f3qAgYB/fYSAe4aAgoCAg4B+fYN+gnuBfYN/e3' +
  'p/fYaBgIB/gX1+f3uAgYJ+f4B/gHt/gX5/fYOAg3uAe3yAe4J/e4N/fIB/gIGAhH+Aen2BhX9/e4' +
  'J+gH6DgX99fYCCfH+Cfn98fH+AfYCDgH17gH97gIGAf4SDgH19gYN+fn5+f3yAg4R/hH+AfYCAhY' +
  'B/e358f3+Cg3+BfX99gH+Df319f3x9f36Agn2AgoF/hIJ/e36AgH+DfX9+fX6Cf4KDgICAgIF+gY' +
  'SAgoJ/g3+BgH+Bfn+Ef35+gIB+gHyAgX+AgYJ/fYF/f4B+fYCCgIB8gH2BgIOBfn6AfH+AfYB/f3' +
  '+DfYB+f3+BgIB+gIB/fH+Bg4B+goCCfn6Bf39/gIKAf4F/fYF+goCDgYCBgIF+gH5/goB/goB+gX' +
  '+AgIB/gIJ/gH6AgICAgH9+f4CAf39/f3+AfYB/gYB/foB9f32BgICAfn5/goB+gYF/gIJ+f4KAgn' +
  '9/f3+Af36Af4B/gICAgH9/gX9+f3+AgH+Afn+AgYB/f4CAgH9/f4F/gX+Af3+BgICAgIB/gH9/gX' +
  '9+gH9/gIF/gH9/gYB/f4B/f39/gIB/f4B/f4B/gH+Af3+Bf4CAgIB/gICAf4CAf4CAgH9/f4CAgI' +
  'B/gIB/gH9/gH+AgICAf4CAgH+Af4CAgIB/f3+AgH+AgH9/f3+AgH+Af4CAgICAf3+Af3+AgICAgI' +
  'B/f4B/gIB/f4CAgICAgH+AgH+AgH+AgIA=';
const SCENE_DING_URI =
  'data:audio/wav;base64,UklGRjYPAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YR' +
  'IPAACAnquihWZVWnSUqaeRcVlWaYijqpt8YFRgfZuqo4hpVlpxkaeok3RbVmeGoaqcf2JVX3qYqa' +
  'OKbFhZb46lqJV3XVZmg56pnoJlVl54laekjW9aWW2Lo6iXeV9XZICcqJ+EaFhddZOmpI9xW1lriK' +
  'GnmHxiV2N+maegh2pZXXOQpKWRdF1ZaYafp5p/ZFhie5emoIltWlxxjaKlk3dfWWiDnKabgWZZYX' +
  'mUpaGLb1xcb4uhpZR5YVpmgZqlnINpWmB3kqOhjXJeXG2In6SWfGNaZX6YpJ2Ga1xfdI+ioo90X1' +
  'xshp2kl35mW2R8lqOeiG5dX3KNoKKRd2FcaoObo5mAaFxjepOinopwXl9xi56ik3ljXWmBmaOag2' +
  'pdY3iRoZ+McmBfb4idoZR7ZV1of5eim4VsXmJ2j5+fjnViX22Gm6GVfmdeZ32UoZuHbl9idIyen4' +
  '93Y19shJmhloBpX2Z7kqCciXFhYXKKnJ+ReWVfa4KXoJeCa19leZCfnIpzYmFxiJufkntnYGp/lZ' +
  '+YhG1gZHeOnZ2MdWNhb4aZn5N9aWBpfZOemYZvYmR1jJydjndlYW6El56Uf2phaHyRnZqIcWNkdI' +
  'qbnY95Z2JtgpaelYFsYmd6j5yaiXNkY3KImZ2Qe2hibICUnZaDbmNneI2bmot1ZWNxhpickn1qYm' +
  't+kpyXhXBkZnaLmpqMd2dkb4SWnJN/bGNqfJCbl4dyZWZ1ipmajnloZG6ClJuTgW1kaXuPmpiIdG' +
  'Zmc4iXmo97amRtgJOblIJvZWl5jZmYinZnZnKGlpqQfWtlbH+RmpWEcWVod4uYmIt3aGZxhJWakX' +
  '9tZWx9j5mVhnNmaHaJl5iMeWpmcIKTmZKAbmZre46Zlod0aGh1h5aYjXtrZm+BkpmSgnBmanqMmJ' +
  'aIdmloc4aVmI59bGduf5CYk4NyZ2p4ipeWinhqaHKEk5iPfm5nbX2PmJSFc2hqd4mWlot5a2hxgp' +
  'KYkIBvaG18jZeUhnVpanaHlJaMe2xocIGRl5GBcWhse4yWlIh2aml1hpOWjXxuaHB/j5eRg3JpbH' +
  'mKlZWJeGtpdISSlo5+b2lvfo6WkoR0amt4iJSVinlsanODkZaPf3Bpbn2MlZKFdWtrd4eTlYt7bW' +
  'pygZCVj4Fyam57i5STh3dsa3aGkpWMfG9qcYCOlZCCc2tteoqUk4h4bWt1hJGUjX5wanB+jZSQg3' +
  'RrbXmIk5OJem5rdIOQlI1/cWtwfYyUkYV2bG14h5KTintva3OBj5SOgHJrb3yKk5GGd21td4WRk4' +
  't8cGxygI2Tj4J0bG97iZKRh3hubXaEkJOLfnFscn+Mk4+DdW1veoiSkoh6b211g4+TjH9ybHF+i5' +
  'KPhHZtbnmGkZKJe3BtdIGOko2Ac21xfIqSkIV3bm54hZCSinxxbXOAjZKNgXRtcHuJkZCGeW9ud4' +
  'SPkYp9cm1zf4ySjoJ1bnB6h5CQh3pwbnaDjpGLf3Nucn6KkY6Dd29weYaQkIh7cW51go2RjIB0bn' +
  'J9iZCOhHhvcHiFj5CJfHJudYCMkYyBdW9xfIiQj4V5cG94hI6QiX1zb3R/i5CNgnZvcXuHj4+Gen' +
  'Fvd4ONkIp+c29zfoqQjYN3cHF6ho6Ph3tycHaCjJCLgHRvc32Jj42EeHBxeYWOj4h8cnB1gYuPi4' +
  'F1cHN8iI+NhXlxcXiEjY+IfXNwdYCKj4uBdnBye4eOjoZ6cnF4g4yPiX50cHR/iY+MgndxcnuGjY' +
  '6Ge3Jxd4KLjop/dXF0foiOjIN4cXJ6hY2Oh3xzcXaBio6KgHZxdH2HjoyEeXJyeYSMjoh9dHF2gI' +
  'qOioF3cXN8ho2MhXpzcniDi46IfnVxdX+JjouCeHJze4WNjYZ7c3J4gouNiX92cnV+iI2Lg3lyc3' +
  'qFjI2GfHRyd4GKjYmAd3J1fYeNi4R6c3N6hIuNh311cneAiY2KgXdydHyGjIuEe3NzeYOLjYd+dX' +
  'J2f4iNioJ4c3R8hYyMhXt0c3mCioyIf3Zzdn6HjIqCeXN0e4SLjIZ8dXN4gYmMiIB3c3Z+h4yKg3' +
  'p0dHqEi4yGfXVzd4CIjImBeHN1fYaLi4R7dHR6g4qMh352c3d/iIyJgXl0dXyFi4uEfHV0eYKJi4' +
  'd/d3R3f4eLiYJ5dHV7hIqLhXx1dHmBiYuIgHh0dn6Gi4mDenV1e4OKi4Z9dnR4gIiLiIB4dHZ9hY' +
  'uKg3t1dXqDiYuGfnd0eICHi4iBeXR2fYWKioR8dnV6gomLhn93dHd/h4qIgnp1dnyEioqEfHZ1eY' +
  'GIiod/eHV3foaKiYJ6dXZ7g4mKhX13dXmAh4qHgHl1d36FiomDe3Z2e4OJioV+d3V4gIeKh4F5dX' +
  'd9hImJg3x2dnqCiIqGf3h1eH+GioiBenZ3fISJiYR8d3Z6gYiKhn94dXh+homIgnt2dnyDiYmEfX' +
  'd2eYCHiYeAeXZ4foWJiIJ7dnZ7goiJhX54dnmAhomHgHp2d32EiYiDfHd2e4KIiYV+eHZ5f4aJh4' +
  'F6dnd9hIiIg313dnqBh4mGf3l2eX+FiYeCe3d3fIOIiIR9eHZ6gYeJhoB5dnh+hYiHgnt3d3yCh4' +
  'iEfnh3eoCGiYaAend4foSIiIN8d3d7goeIhX55d3l/hYiGgXt3eH2DiIiDfXh3e4GHiIV/eXd5f4' +
  'WIh4F7d3h9g4eIg314d3uBhoiFgHp3eX6EiIeCfHh4fIKHiIR+eXd6gIaIhoB6d3l+hIiHgnx4eH' +
  'yCh4iEfnl3eoCFiIaBe3d5fYOHh4N9eHh7gYaHhX96d3p/hYeGgXt4eH2Dh4eDfXl4e4GGh4V/en' +
  'h5f4SHhoJ8eHh8goaHg355eHuAhYeFgHt4eX6Eh4aCfHh4fIKGh4R+eXh6gIWHhYB7eHl+g4eGgn' +
  '15eHyBhoeEf3p4en+Eh4WBe3h5fYOGhoN9eXh7gYWHhH96eHp/hIeGgXx5eX2ChoaDfnl4e4CFh4' +
  'WAe3h6foOGhoJ8eXl8goaGg356eHuAhIeFgHt5en6DhoaCfXl5fIGFhoR/enl7f4SGhYF8eXp9go' +
  'aGgn16eXyBhYaEf3t5en+EhoWBfHl5fYKGhoN+enl8gIWGhIB7eXp+g4aFgX15eX2ChYaDfnp5e4' +
  'CEhoSAe3l6foOGhYJ9enl8gYWGg397eXt/hIaFgHx5en6ChYWCfXp5fIGFhoR/e3l7f4OGhYF8eX' +
  'p9goWFgn56eXyAhIaEgHt5e3+DhoWBfXp6fYKFhYN+e3l8gISGhIB8ent+g4WFgn16en2BhYWDf3' +
  't6e3+EhYSAfHp6foKFhYJ+enp8gYSFg397ent/g4WEgXx6en6ChYWCfnt6fICEhYN/fHp7f4OFhI' +
  'F9enp9gYWFgn57enyAhIWEgHx6e36ChYSBfXp6fYGEhYN/e3p8gIOFhIB8ent+goWEgn57en2BhI' +
  'WDf3t6fH+DhYSAfXp7foKEhYJ+e3p9gISFg398ent/g4WEgX16e36BhIWCfnt6fICDhYOAfHp7f4' +
  'KFhIF9e3t9gYSFgn97enyAg4WDgHx6e36ChISBfnt7fYGEhIN/fHp8f4OEg4B9e3t+goSEgn57e3' +
  '2Ag4SDf3x7fH+ChISBfXt7foGEhIJ+e3t9gIOEg4B8e3x/goSEgX17e32BhISCf3x7fICDhIOAfX' +
  't8foKEhIF+e3t9gYOEgn98e3x/g4SDgH17fH6ChISBfnt7fYCDhIJ/fHt8f4KEg4B9e3t+gYSEgn' +
  '58e32Ag4SDgHx7fH+ChIOBfXt7foGDhIJ/fHt9gIOEg4B9e3x/goSDgX58e32Bg4SCf3x7fX+ChI' +
  'OAfXt8foGEg4F+fHt9gIOEgn98e3x/goSDgH17fH6Bg4OBfnx7fYCDhIJ/fXt8f4KEg4F+fHx+gY' +
  'ODgn98e32Ag4SCgH17fH+Cg4OBfnx8foGDg4J/fHt9gIKDg4B9fHx+gYODgX58fH2Ag4OCf318fX' +
  '+Cg4OAfXx8foGDg4F+fHx9gIODgn99fH1/goODgH58fH6Bg4OBf3x8fYCCg4KAfXx8f4KDg4F+fH' +
  'x+gYODgn99fH2AgoOCgH18fH+Bg4OBfnx8foCDg4J/fXx9f4KDgoB+fHx+gYODgX58fH6AgoOCf3' +
  '18fX+Cg4KAfnx8foGDg4F/fXx9gIKDgoB9fH1/gYODgX58fH6Bg4OBf318fYCCg4KAfXx9f4GDg4' +
  'F+fHx+gIKDgn99fH1/goOCgH58fX+Bg4OBfn18foCCg4J/fXx9f4KDgoB+fH1+gYKDgX99fH6Ago' +
  'OCgH18fX+Bg4KAfnx9foGCg4F/fXx9gIKDgoB+fH1/gYOCgX59fX6AgoOBf318fX+Cg4KAfnx9f4' +
  'GCgoF+fX1+gIKDgX99fH1/gYOCgH59fX6BgoKBf319foCCg4J/fXx9f4GCgoB+fX1+gYKCgX99fX' +
  '6AgoKCgH59fX+BgoKAfn19foCCgoF/fX1+gIKCgoB+fX1/gYKCgX99fX6AgoKBf319fX+BgoKAfn' +
  '19f4GCgoF/fX1+gIKCgX9+fX1/gYKCgH59fX6AgoKBf319foCCgoGAfn19f4GCgoB+fX1+gIKCgX' +
  '99fX6AgYKCgH59fX+BgoKAf319foCCgoF/fn1+f4GCgoB+fX1/gYKCgX99fX6AgoKBf359fn+Bgo' +
  'KAfn19f4CCgoF/fX1+gIGCgYB+fX1/gYKCgH59fX6AgoKBf319foCBgoGAfn19f4GCgoB/fX1+gI' +
  'KCgX9+fX5/gYKBgH59fX+BgoKBf319foCBgoF/fn1+f4GCgYB+fX1/gIKCgX99fX6AgYKBgH59fn' +
  '+BgoKAfn19f4CCgoF/fn1+gIGCgYB+fX5/gYKCgH99fX6AgYKBf359fn+BgoGAfn1+f4GCgoB/fn' +
  '1+gIGCgX9+fX5/gYKBgH59fn+AgoKBf359foCBgoGAfn1+f4GCgYB+fX5/gIGCgX9+fX6AgYKBgH' +
  '59fn+BgoGAf359f4CBgoF/fn1+gIGCgYB+fX5/gIGBgH9+fX6AgYKBf359fn+BgoGAfn5+f4CBgY' +
  'B/fn5+gIGCgX9+fX5/gYGBgH9+fn+AgYGBf35+foCBgYGAfn5+f4GBgYB/fn5/gIGBgX9+fn6AgY' +
  'GBgH5+fn+AgYGAf35+f4CBgYF/fn5+f4GBgYB+fn5/gIGBgH9+fn6AgYE=';
const SCENE_POP_URI =
  'data:audio/wav;base64,UklGRgQEAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0Ye' +
  'ADAACAgIGDhISDf3t2c3J0eYCIkJSVkop/c2liYWZwf46cpaehlYRxX1NPVGF1jKGwtrOlknpkU0' +
  'pKVGV8k6aztrGijndhUUlKVWZ8kqaytrGkkHplVEpJUWF1jKCvtrWrmoVvXE5JTFdofZOlsrazp5' +
  'aBa1lNSUxXaX2SpbG2tKmZhHBdUElKU2J2ip2stbavopB8aFhNSUxXZ3qOoK61tq+ikHxpWE1JS1' +
  'RjdYmcqrS2sqiYhnJhU0pJTlloe46frbW2saeXhXJhU0tJTVZldomaqbO2tKyfjnxrW1BJSU9aaH' +
  'qMnKqztrStoJB/bV5SS0lMVWJyg5SjrrW2sqmcjXxrXFFKSUxUYXCAkaCss7a0rqOVhXRlWE5JSU' +
  '1WYnGBkZ+rs7a1sKaZintrXlNMSUpQWWZ0hJOgq7O2trGonY+AcWNXT0pJTFJcaXeGlKGrsra2sq' +
  'uglIZ3al1TTElJTVReaneFk5+psbW2tK6mm46Bc2ZbUkxJSUxTW2ZzgI2ZpK2ztrazrqWbj4J2al' +
  '9VTkpJSk5VXWh0gIyYoquxtba1sauimI2BdmpgV1BLSUlLUFdganWAjJagqa+0tra0sKmhmI6DeG' +
  '1jW1NOSklJTFBWXmdxe4aQmqKqr7S2trWyraafloyCeG9mXVZQTEpJSUxQVVxjbHV+iJGZoaiusr' +
  'W2trWyraihmpKJgHhvZ2BZU09LSUlJS05SV11kbHR8hIyUm6KorbG0tra2tbKvq6WgmZKLg3x1bW' +
  'dgW1ZRTktJSUlKTE5SVltgZm1zeoCHjpSaoKWprbCztba2trW0sa6rp6KdmJONh4F7dnBrZWFcWF' +
  'RRTkxKSUlJSUpLTVBSVlldYWVqbnN4fYKHi5CUmZ2gpKeqra+xs7S1tra2tbSzsrCurKqnpaKfnJ' +
  'mVko+LiIWBfnt4dXJvbGlnZGJgXlxaWVdWVVRTU1JSUVFRUVJSUlNUVFVWV1hZWltcXV9gYWNkZW' +
  'doaWtsbm9wcnN0dXd4eXp7fH1+gICBgoOEhYaGh4iJiYqKi4uMjI2Njo6Oj4+PkJCQkJCQkZGRkZ' +
  'GRkZGRkZGRkZGQkJCQkJCPj4+Pjo6Ojo2NjYyMjIuLioqKiYmIiIeHhoaFhYSDg4KCgYGAf39+fX' +
  '18fHt6enl4eHd3dnV1dHRzc3JycXFwcHBvb29vbm5ubm5ubm5ubm9vb29wcHFxcnJzc3R1dnZ3eH' +
  'l6ent8fX5/f4CBgoODhIWFhoaHh4iIiIiJiYmJiYmIiIiHh4eGhoWFhISDgoKBgYCAf39+fn19fX' +
  '18fHx8fHx8fHx9fX19fn5+fn9/f39/f3+AgA==';
const SCENE_WHISPER_URI =
  'data:audio/wav;base64,UklGRl4RAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YT' +
  'oRAACAf3+AgH+AgICAgIB/gIB/gH+Af4CAgICAgICAf4CAf3+Af4CAf39/gIB/f3+AgH9/f4B/f4' +
  'CAgH9/f3+AgH9/gICAgIB/gIB/gH9/gH9/f4B/gIB/f3+Af39/gH9/f3+Af4CAgICAf3+AgH9/gH' +
  '+Af3+AgH+AgH9/f39/f39/f4CAgIB/gIB/gH9/f39/gICAgICAf39+gIB/f4B/gH9/gX+Bf4B/f3' +
  '9/gIB+f39+f39/f4CAf36AfoCBgH6AfoGBfoCAf3+AgICAf35+foB+foB/gYGBfn6AgYCBgH5+f4' +
  'B/gH+AgIB+f3+BgIB+fn6BgH5+fn5+foCBgoGAf4F/gX9/gH99foF+fYCAgIB/gH6CgoKAgIGAgI' +
  'GAgIB/gH19gYB+gX99gX5/gX2Cf3+Bf3+CgX6BgX2Af4GBf39+gH2AgH1/gn+AfoB+gIJ+fYF+gH' +
  '5/fIF+goF9gYF8fYCAgoJ9gYJ9goJ+g4F9fn6Dgn9+fYJ/foOCfnyAgYN/g31/fYN+gH19foGAfH' +
  'x9f36AfYF8fYOBg36CgYCAg4B9fIN8g319fn6AgHx/goJ9gIB9fnyAfoN8fICDgn+Bf4B/g35/fo' +
  'KDf4CBgn1+g4SCfHuEfXuBfHuAfYR9gH59f36BgoOAg4OCfYR/fISDgn2Agn9/goOBfn5/e3+Ee3' +
  '2De4SAhHx+hICEfIB7hIOBe3yEgnx9gHt+gIOCf36CfoGAen99fnuAgISCgH2Be3qDfnyAgoCFhI' +
  'B7g3+Df32Ae4GFhIJ/fX6Ae4V7fn+CgXt+eoCChHqBfnyEhH98g4J/gYB6gHyEgHyAg4B7e4J6g3' +
  'uAg4V/e3+Agn98fIOFfn6Cg4SDgoCBfoV6gYCEhICDf4WBg4R8hX59hX6BgXuAg4N7f316g4KBf3' +
  '98eoKAg4R/e4J7g39/g4F7foV/gIN8foJ+fH2EfH59e4F6e4KEhYJ8hIV9f4B/g4GDfn59hH6FgX' +
  'x/foR7eoN6gHt8hH5+g4ODfIJ8g3+BgX97gYWFeoKCen99hYV7fYCEf4J9gnp/e3p6fnyEfXx+gn' +
  'p6gX+Af3x+hH2BhYV/fHx6f359f3x9e358fYF+f3x7gH6CfYGAe4F9fXt9gn1/g4KChIR8hHuEhH' +
  '5+g4CBhHt/foF7e3p8fX5/gYOAgHt8g359gHx+fIGBfoGAgH18f398gYR9g3uEfX2CgICDg4N/fo' +
  'KBgoCBg4OEfHx+f4GDhIN8g3+CgXyBgn2Bgn+DhHt8fH98gH+Df4OCgn58f4B+gIF/gICCgoN+g4' +
  'F9f4F9fX99fYKBf31+gn2BgoCBgnx9fX18gIJ9fn59fn5+gYJ9f4GBfX99f4KBgoJ/f4J9goJ/f3' +
  '+CgH6Cf35/foCBgH6AfYGBgH5+gH+CgX2BfoF/fn99fYF9foB+f32AgH+BgH5/f4B+gIJ/gIGAgY' +
  'CBfYJ+f36Bf3+AgIB/gH6BgYB+f4CBf4CBf4GAfn6Af4B/f35/f35/gH6AgX9/gH9/gYB+gYB/gI' +
  'CAgH+Afn9+f4CAgH9/gH+BgIGAgIB/gH9/f3+AgH+AgICAgICAf3+AgH9/gICAgH+AgH+Af4B/gH' +
  '9/f39/gIB/f39/gICAf39/gIB/gICAgICAf4CAf4B/gICAgH9/f39/gH9/f3+AgICAgH+Af3+Af4' +
  'CAf4CAf3+Af39/f4B/gH9/gH+AgH9/gH+Af4CAgICAf4B/f4CAf4CAf39/f4CAf4B/gH9/gIB/f4' +
  'CAgH+AgIB/f4B/gH+Af3+Af4CAgIB/gH9/f4CAf4B/gH+Af4CAgH+Af39/gICAf4CAf4B/gH9/gH' +
  '+AgICAgIB/gH9/f39/f4CAf39/gICAf39/gH9/gH+AgIB/f39/gIB/gH9/gH+Af3+AgIB/gH+AgI' +
  'B/gH9/gICAf4B/gH9/f39/f4B/gIB/f4B/f4CAgH+Af39/gH+Af4B/gH+AgH+AgH+AgH+Af4CAgI' +
  'CAgIB/gH+Af4CAf39/f4B/f3+Af39/gIB/gH9/f4B/gICAgH9/f4CAf4B/gH9/gH9/f39/gH+AgH' +
  '9/gH+AgH9/gICAf3+AgICAf4B/f4B/f4B/f4CAgH9/gIB/f39/f4B/gICAgIB/gH+Af39/f3+Af3' +
  '+AgICAf39/f4B/fn9+gICAgIB+f3+Af4CBfn6AgYB+gH9+foB/f4CBgICAgH6AgICBfn6AfoGAf4' +
  'B/foGAf4CBgICAf36Bf4B+fn5+fn+Bfn5/gX6AgX+Bf4B/f4B+gYKBgYGAgYCAfX5+gIF+goB/go' +
  'GAf31+gX9/fn9/f31+goF9f4J+gIKAfX+Bf4KAgYCAgoKBfn9/foF+fn1/f4GCfYF+f358gH6Bf3' +
  '1/f32BgX+Bf4J+f4J8fn2BgIJ8fnx+fIF8fH19fHx8gHyDfH+AfYJ/g35+gX+DfX2Dg4CCg4GCgH' +
  '2Dfn9+gIF8g4B8fX17gIKEg3+Dfn1+foB8fYR8g4F7gnyAhHyDf4J+g32Cfnx+e4CDfH+Cfn5+go' +
  'R/fnx9gnt/f3uEg4V+hIJ8gYN9goSBf4KBe318fYODe4J9fn57e4CEgoOFfnp9g39/g4OFfoGDgn' +
  '18hHt9g32Ce4V/hYGBgXt8en1+e36EhX6Bg4GAhX19f3mAgoWBhnqBfXt+g3x8gXt6gn97g318en' +
  'qCgIaFhH15goF8eXqAhoSGfoZ8e3qChoN6eoN7g356hISAgYV/hXl7g4WChYaBeXp+gIWFfXyGhX' +
  'p4eXl6goSAhIB9en6CeXp7hIGEenmDfHyBhXuAfIGGgH6Bh4N9gYSFg399eYKAgod4eoCEenl6hI' +
  'KHfHqFfHp9fIGGeYF8foKBgYB9enh/h4h+hHmGgn6Ffn2Ben18hH2EhX6EgXx4hXl8f4KEd4V8fo' +
  'N4iIJ5fHh4f4B7e4J8e4GHhoN6hYGFeHt/g4J7g4KCd3x5f3uFgn+Gh3l9gX5+g4h+h4aHeHmCd3' +
  'h/gYJ7eYWHhoeAd4CAg4OEenqHh4KDeIF6gHiFe32BhX5+fIaGfoKHhod4fX16iHl7g3x/hYB+fX' +
  '6DeIJ8eHiBhIOHe4KEfn6Egnp6hoZ7hYR8eniAfod5g3x+f3p4hIGAh3uEfYd+gXmCe32Hgod3gI' +
  'SIgnyEgYZ8d36Ienh/h3l7hoKBd4B9fn+BeYeGf3qAeIKDgIJ8hYF7f4J8eIJ6fICEg4R5fIaGfH' +
  'iCgoSDfoWBhoR/d3iGe3x+eoGBfXeDh32FeYZ4gYh+fn54h4N8h316hHl6g4GCf4V4fX+Ehn98gH' +
  'uCfYGHgoJ9h4J6gYKHfnx3hHx+fH+AhXh+h39/fnt7fX56fYR7hISFe4Z9eYR9hoGAg4V7enqCgY' +
  'WHhXx+gn9/gnp8hYV5hH2FgoJ4e3uFhYN9fnh8fnl5e3p6f32Df3uEfoKEgnyBhHqDgn98hIB8hY' +
  'V8gH58g3mFhX6ChHp9f4B/hn+BgnmCfnp/hYWEhoCCgnl/gH5/hH19gX2Gf4ODhoR9hn99hIV/gH' +
  'uChX98fnx5gnt+fnqAgISDhX57gX56g4F6gYWDeoSChYWAfYF/g4R9g4CCf399hIB9gYF9fnt8fI' +
  'J/gn2BeoCBfX6Cg31/gIWEfH2AgYJ+g3x/hIGBgn2AgH17gYGCe3+AfX97gYF9fn5+gYF7gX9/fX' +
  '6Ee3t9fYSAe36EgHuAgn99gn2Ee36EgIN7foODgH17fIJ+f357fHyDfnx9foKDg4N/fICAfX1+f4' +
  'B/g39+fIJ9gX2Cf3+AfH19fn2AfoB+gn59fnx+f4CBgH59gH5+fn5+gH2BfYGBgoGBgICCf4F/go' +
  'F9foKAfoB9gH5/fn1/foGBfn5+foF/fX+BgYJ+gYCBfoCAf4GAgYGAfoCAfn6BgYGBfn5+gH99gH' +
  '6AgYF/f4F+f3+AfoF/foCBf4F+gIF+foCAgH5+f3+BgH6BgH+Bf35+gX6Bf4B/f35/f4CAf4CAfn' +
  '+AgH9+gICAgIB/gIB/gIB/f4CBgICAgYCAgIB/gIB/f4GAf3+Af4CAgH9/gH9/f4B/gICAgH+AgI' +
  'B/gH+AgH+AgH9/f3+Af39/f3+AgH9/gIB/gH+AgICAgH+Af4CAf3+AgH9/f4B/f3+Af3+AgH9/gH' +
  '+AgIB/gH+AgH+Af3+AgH9/f4B/gH+AgH+AgH9/f39/gIB/gH+Af39/gH9/f4B/gH+AgH9/f4CAf3' +
  '+Af39/gIB/f4B/f4B/gH9/gH9/gH+Af39/f4B/f4CAgICAf39/f4CAgH+AgH9/gH+Af39/f4B/f4' +
  'CAf39/gH9/f4B/gIB/gH+AgH+Af3+Af39/gH+Af4CAf39/f39/gH+AgH9/f3+AgH+Af39/f39/gH' +
  '9/f3+Af3+AgIB/gIB/f4CAf3+AgIB/gIB/f4CAgH+Af4CAgH+Af3+AgIB/f4CAgICAgICAf39/gI' +
  'B/f4B/gH9/gICAgH+Af3+Af4B/gH9/f4B/f4CAf3+Af4B/gH9/f39/f3+AgIB/f39/gH+AgH9/f3' +
  '9/gH9/f39/gIB/gH9/f39/f3+Af4CAgIB/gH9/f39/gH9/f39/f4CAgH+Af4B/gH+Af39/f4B/f3' +
  '9/gH9/f3+AgH9/f4B/gH+AgIB/gH+AgH+AgH+AgICAf39/f4B/f3+AgIB/f39/f4B/gICAgH9/f4' +
  'B/f4CAf4B/gH9/f4CAf4B/f4B/f4B/f4B/foB/fn6BgX9/gH6Bf3+AgICAgIGAf3+Af39/gIB/f3' +
  '+Af3+Af4B/gYCAgX9+gIB/gH9/f4GAf3+AgIGAf39/gYCAgYB/foGBf4F/gICAgX+AgICBf3+Bf3' +
  '+Bfn+BgH9/fn9/gIB/gYCAgX9+gH+AfoF/gH9/fn9+foF/gH9/gX5/fn+Af4B+fn+AgYCBgX+Af4' +
  'B+f4F/gX+Bf3+AgH9+foF/gH5/gYF/f39/f36BgX9/fn+Bf35/gYB/gX5+foGBgH9/gIF/fX5/gH' +
  '9/gYCBgIF+gX6BgH+BfYB+foCAgH5/gX6AgH99gH+AgX6AgoCCfn9+fn+Cf4B+foCCgX+AgYB+f3' +
  '5+gH1+fYGBgX5/fYJ+foF/f39/foF9fn9+gIF/gX9/foJ/gn6BgICBf3+BgH1+gn9+gX59gYGAfn' +
  '+AgH5/f35/fn1/fn6AgH5+gH9+gX+BfoCAf4B/gICAf4B/gIKAgX6AgX59f31+f35+gYGBf36BgY' +
  'B/gH9+gH6Afn+Af32Bf39/foGBfYF/f4GAfn+Cgn6CgIF+foGAf35+fX9+foCCgH+Af32Bf4CBf4' +
  'F/fn5+gYF/gIF+fn6AgX1+gYF+gH2Afn9+foF+gX99fn+BfoF+gX9/gYCAf4B+gYCAf4B+fn5+fn' +
  '+Bf35+gYB9gX1/foCBgIF/gX9+gIGAgH+BgH9+f4B+gX+Bf3+BfoB/gX5/f36AgYGBgIB/gH+Af3' +
  '5+f3+AgYB+gYGAfoB/gYF+gYF/gH9/foB+gH+Afn6Bfn9+f3+AgYCBfn+Af4F/gYB/gH6BfoGBgY' +
  'B+fn6AgH9/gYB/gICAf4B/f3+Af3+AgICAgIF/fn9/gH9/gICAgH+Af3+Afn+AgYF/f39/gIF+gH' +
  '+AgH+Af3+AfoCAf36Afn5/f4GAf35+f4B+gH+Af4CAgYB/gH+Af4B/gH9/gICBf3+AgIB/gH5/f4' +
  'CAgIB/f4CAgH9/f39/f4CAf39/gICAgH+AgICAf39/gH9/gH+AgH9/gH+Af39/gH9/f4B/gH9/f4' +
  'B/gIB/f39/gICAgIB/gH9/f4CAgH9/gIB/gICAgH+AgIB/gICAf39/f39/f4CAf39/gH9/f39/f4' +
  'CAf4CAgH+AgICAf4B/f3+Af4CAf3+Af4B/f4B/gH+AgIB/gH9/gH9/f3+Af39/gICAf3+Af3+AgH' +
  '9/gH+AgICAf4CAf4B/gICAf39/gIB/gH8=';
const SCENE_BINLAP_URI =
  'data:audio/wav;base64,UklGRk8FAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YS' +
  'sFAACArsCthWdqjrvTx591ZnidubWRY0dMbI2XgFc4NlN7lI9xUkphiq20nn5rdZa5xrWRc22CoL' +
  'GmhGBPWnaOj3dVQEVigpGGa1VVbpGpqZN5cH6ctbqmiHN0iKCpmntgV2V9jIdwVUlTbYWMf2laYH' +
  'iUpJ+LeHWFnrCum4J1eo2eoJB2Yl9ugYqBbFhSXnWGiHpoYGl/lZ6WhXh6ip6ppJJ/d3+Pm5mIcm' +
  'VmdIOHfGpbWmh6hoN3aWZxhZWZj4F5fo2do5uLfXqDkJeSgnFobXmDg3hpX2JvfYSAdWtsd4iTk4' +
  'p/e4KPm52Uhn19hpCTjH5xbHJ8g4B2aWRodH+DfXRucXyJkY+Gfn2FkJiXj4N9f4ePj4d7cXB2fo' +
  'J+dGtobniAgXt0cXV/io6LhH5/h5CVkoqBfoGIjYuDeXNzeX+BfHNtbHJ7gIB6dHN5goqMiIJ/gY' +
  'iPko6HgH+CiIuIgXl0dnuAf3pzb3B2fYB+eXV2fIOJiYWBf4KJjo+LhICAg4iJhX94dnh9gH55dH' +
  'FzeX6AfXl3eH6EiIeEgICDiYyMiIOAgYSHh4N9eXh6fn99eXVzdnt+f315eHuAhIeFgoCBhIiLiY' +
  'aCgIGEhoWBfXl5fH5/fXl2dnh8f398enp8gYSFhIGAgYWIiYeEgYCChIWEgHx6e31/fnx5d3d6fX' +
  '9+fHt7foGEhIOBgIKFh4eGg4GAgoSEgn98e3x+f358eXh5e35/fnx7fH+ChIOCgYGChYaGhIKAgY' +
  'KDg4F+fHx9fn9+fHp5enx+f358fH1/goODgYCBgoSFhYOBgIGCg4KAfn18fX5/fXx6ent9fn5+fX' +
  '1+gIKCgoGAgYOEhYSCgYCBgoKBgH59fX5/fn18e3t8fn9+fn19f4CCgoGBgIGChISDgoGAgYKCgX' +
  '9+fX1+f359fHt8fX5/fn59fn+BgYKBgICBgoODgoGAgIGBgYB/fn5+fn9+fXx8fX5+f35+fn5/gY' +
  'GBgYCBgYKDgoKBgICBgYGAf35+fn9/fn19fX1+f39+fn5/gIGBgYCAgYGCgoKBgICAgYGAgH9+fn' +
  '5/f359fX1+fn9/fn5+f4CBgYGAgIGBgoKBgYCAgIGBgH9/fn5/f39+fn1+fn9/f35+f3+AgIGAgI' +
  'CBgYGBgYGAgICBgIB/f35/f39/fn5+fn5/f39/f3+AgICAgICAgYGBgYGAgICAgICAf39/f39/f3' +
  '5+fn5/f39/f39/gICAgICAgIGBgYGAgICAgICAgH9/f39/f39+fn5+f39/f39/f4CAgICAgICAgY' +
  'GBgICAgICAgH9/f39/f39/fn5+f39/f39/f3+AgICAgICAgIGBgICAgICAgIB/f39/f39/f39+f3' +
  '9/f39/f39/gICAgICAgICAgICAgICAgICAf39/f39/f39/f39/f39/f39/gICAgICAgICAgICAgI' +
  'CAgICAgH9/f39/f39/f39/f39/f39/f4CAgICAgICAgICAgICAgICAgH9/f39/f39/f39/f39/f3' +
  '9/f3+AgICAgICAgICAgICAgICAgIB/f39/f39/f39/f39/f39/f39/gICAgICAgICAgICAgICAgI' +
  'CAf39/f39/f39/f39/f39/f39/gICAgICAgICAgICAgICAgICAgH9/f39/f39/f39/f39/f39/f4' +
  'CAgICAgICAgICAgICAgICAgH9/f39/f39/f39/f39/f39/f3+AgICAgICAgICAgICAgICAgIB/f3' +
  '9/f39/f39/f39/f39/f38=';
const SCENE_PRINT_URI =
  'data:audio/wav;base64,UklGRg8NAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0Ye' +
  'sMAACAf36CgoN7e4WGhnh3iYmKdHSMjY1xcJCQkW1tk5SUammXl5hmZpqbm2Ninp6fX1+hoqJcW6' +
  'WlpllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpq' +
  'ZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWV' +
  'mmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZWa' +
  'amWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpl' +
  'lZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWV' +
  'mmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpq' +
  'ZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpqZZWaampl' +
  'lZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWa' +
  'ampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpq' +
  'ZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWV' +
  'mmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZWaamWVlZpqZZWVmmpllZWa' +
  'amWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpl' +
  'lZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWV' +
  'mmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpq' +
  'ZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmlpaVaWqWkpFtbpKSjXFyjo6NdXaKiol' +
  '5eoaGhXl+goKBfX5+fn2Bgnp6eYWGenZ1iYp2dnGNjnJycZGSbm5tlZZqammVmmZmZZmaYmJhnZ5' +
  'iXl2hol5aWaWmWlpVqapWVlWtrlJSUbGyTk5NsbZKSkm1tkZGRbm6RkJBvb5CPj3Bwj4+OcXGOjo' +
  '5yco2NjXJzjIyMc3SLi4t0dIqKinV1iomJdnaJiIh3d4iIh3h4h4eHeXmGhoZ5eoWFhXp7hISEe3' +
  'uDg4N8fIOCgn19goKBfn6BgYB/f4CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAf36CgoN7e4WGhnh3iYmKdHSMjY1xcJCQkW1tk5SUammXl5hmZpqbm2Ninp6fX1+hoqJcW6Wlpl' +
  'lZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWa' +
  'ampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpq' +
  'ZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZWaamWV' +
  'lZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWa' +
  'amWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpl' +
  'lZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWV' +
  'mmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpqZZWaampllZpq' +
  'amWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampl' +
  'lZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWa' +
  'ampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpq' +
  'ZZWaampllZpqamWVmmpqZZWaampllZpqamWVmmpqZZWaampllZWaamWVlZpqZZWVmmpllZWaamWV' +
  'lZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWa' +
  'amWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpl' +
  'lZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqZZWV' +
  'mmpllZWaamWVlZpqZZWVmmpllZWaamWVlZpqamWVmlpaVaWqWkpFtbpKSjXFyjo6NdXaKiol5eoa' +
  'GhXl+goKBfX5+fn2Bgnp6eYWGenZ1iYp2dnGNjnJycZGSbm5tlZZqammVmmZmZZmaYmJhnZ5iXl2' +
  'hol5aWaWmWlpVqapWVlWtrlJSUbGyTk5NsbZKSkm1tkZGRbm6RkJBvb5CPj3Bwj4+OcXGOjo5yco' +
  '2NjXJzjIyMc3SLi4t0dIqKinV1iomJdnaJiIh3d4iIh3h4h4eHeXmGhoZ5eoWFhXp7hISEe3uDg4' +
  'N8fIOCgn19goKBfn6BgYB/f4CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgI' +
  'CAgICA';
const SCENE_POUR_URI =
  'data:audio/wav;base64,UklGRqwVAABXQVZFZm10IBAAAAABAAEAESsAABErAAABAAgAZGF0YY' +
  'gVAACAgICAgICAgICAgICAgICBgYGBgYGBgYGBgYGBgYGBgYCAgICAf39/f35+fn59fX19fHx8fH' +
  'x8e3t7e3t7e3x8fHx8fX19fn5+f3+AgIGBgoKDg4SEhIWFhYaGhoaGhoaGhoaGhoWFhYSEg4OCgo' +
  'GAgH9+fX18e3t6enl4eHd3d3Z2dnZ2dnZ2dnd3d3h4eXp6e3x9fX5/gIGCg4SFhYaHiImJioqLi4' +
  'uMjIyMjIyLi4uKiomIh4eGhYSDgoF/fn18e3p5eHd2dXRzc3JycXFwcHBwcHFxcXJzc3R1dn6FfX' +
  'FwfYqJfXZ8i5KLgH6Kl5aLg4iWnJWJh5Gcm4+GipWakoaCipOShnx+iIyEeHR7g4F2bW53e3VqZm' +
  'x0dGpjZW5zbmVjanN0bWdqc3p3cG52f4F8d3uEi4mDgomRk4+KjZWamJKQlZydl5KTmZyZko+Slp' +
  'aQiYiMjoqDf4CEg313dXh6dnBsbnFwbGdnamxqZmRmamtoZmdqbm5sbG5zdXR0dXl9fn5+gYWHiI' +
  'iKjY+QkJGTlZaWlpaXmJiXl5eXlpaVlJOSkI+OjIqJh4WEgoB+fHt5d3V0cnFvbm1sa2ppaWhoZ2' +
  'dnZ2hoaWlqa2xtb3Bxc3R2eHl7fX+AgoSGh4mLjI6PkJKTlJWVlpeXl5iYmJeXl5aVlZSTkZCPjY' +
  'yKiYeGhIKAf317eXh2dHNxcG9ubGtqamloaGhnZ2doaGhpaWprbG1ucHFydHV3eXp8fn+Bg4WGiI' +
  'qLjY6PkZKTlJWWlpeXl5iYmJeXl5aWlZSTkpGQjo2LioiHhYOCgH58e3l4dnRzcXBvbm1sa2ppaW' +
  'hoZ2dnZ2hoaGlqamtsbW9wcXN0dXd5enx+f4GDhIaHiYuMjY+QkZKTlJWWlpeXl5iYmJeXl5aWlZ' +
  'STkpGQj42MiomHhoSCgX9+fHp5d3Z0c3Fwb25tbGtqaWloaGhnZ2doaGhpaWprbG1ub3Bxc3R2d3' +
  'l6fH1/gYKEhoeJioyNjo+RkpOUlZWWl5eXmJiYmJeXl5aWmqGRhZGdkYGIlpB+f46Ne3aEiHlueY' +
  'N3aW98dWVndXRkYW5zZV1pcmhdZXFsX2NxcWRkcndsZ3N8dGx1gX1zeYaGfH2KjoSBjZSNho+ZlI' +
  'yQm5qQkZyelJGan5eQlp2Yj5Kal42MlJSKho2Ph4GFiYN7fYJ/dnV7enJvdHVuam5xbGZpbWtlZW' +
  'tqZWRpa2dlaW1qZ2twb2xuc3Rxcnd6eHh8gH9+gYWFhIeLjIqMj5GQkJOVlJSVl5eWl5iYl5eYmJ' +
  'eWlpaVlJOSkZCPjo2LiomHhoWDgoB/fXx6eXh2dXNycXBvbm1sa2pqaWloaGhnZ2dnaGhoaWlqa2' +
  'tsbW5vcHFydHV2eHl6fH1/gIKDhIaHiYqLjI6PkJGSk5SUlZaWl5eXmJiYmJiXl5eWlpWUlJOSkZ' +
  'CPjo2LiomHhoWDgoF/fnx7enh3dnRzcnFwb25tbGtqamlpaGhoZ2dnZ2hoaGhpaWpra2xtbm9wcX' +
  'JzdXZ3eHp7fX5/gYKDhYaHiYqLjI6PkJGSkpOUlZWWlpeXl5iYmJiYl5eXlpaVlZSTkpGQj46NjI' +
  'uKiYeGhYOCgX9+fXt6eXd2dXRzcXBvbm5tbGtqamlpaGhoZ2dnZ2doaGhoaWlqa2tsbW5vcHFyc3' +
  'R1dnh5enx9fn+BgoOFhoeIiouMjY6PkJGSk5OUlZWWlpeXl5iYmJiYl5eXl5aWlZSUk5KRkZCPjo' +
  '2LiomIh4aEg4KAf359e3p5hHBsf3dlc3tnZ3luX3B0YGR1Z11ub11lc2RecG1daHRkYnRvYW93aG' +
  'l6c2h3fW5zgnhxgYN2fYp/e4uKfoiShoWUkIaRmIyNmpSMmJyQk56WkJydkZaflpGcm5CWnZKQmp' +
  'aMk5eMi5SOho6PhYaNhn+Ghnx+hHx4f310d3tzcXd0bXFzbGtxbWdsbmdnbGhlampkZmtnZWppZW' +
  'hraGdsbGlsb2xtcXBvcnRydHh3d3p7enx/f3+Cg4OFh4eIioqLjI2Oj5CRkpKTlJSVlZaWl5eXl5' +
  'iYmJiYmJeXl5eWlpWVlJSTkpGRkI+OjYyLiomIh4aFhIOCgH9+fXx7enl4d3Z1dHNycXBvbm1tbG' +
  'tramppaWloaGhoZ2dnZ2doaGhoaWlpampra2xtbW5vcHFycnN0dXZ3eHl6fH1+f4CBgoOEhYaHiI' +
  'mKi4yNjo+QkJGSk5OUlJWVlpaXl5eXl5iYmJiYmJeXl5eWlpWVlJSTk5KRkZCPjo2NjIuKiYiHho' +
  'WEg4KBgH9+fXx7enl4d3Z1dHNycXBwb25tbWxra2pqaWlpaGhoaGhnZ2dnZ2doaGhoaWlpampra2' +
  'xsbW5ub3BxcnJzdHV2d3h5ent8fX5/gIGCg4SFhoeHiImKi4yNjo6PkJGRkpOTlJSVlZaWlpeXl5' +
  'eXmJiYmJiYl5eXl5eWlpaVlZSUk5OSkZGQj4+OjYyLioqJiIeGhYSDgoGAf39+fXx7enl4d3aDbX' +
  'B/aHB8ZXB4YnF0YHJwX3JsXnNpXnNmYHNjYXNhY3JfZnJfaXFfbHBgb3Bhcm9kdm9neG9qe29ufX' +
  'Byf3F3gXJ7g3WAhHeEhXqIhn2Mh4CPiISSiYiUiouWi4+XjJKYjZWZjpiZkJqYkZyYkp2Xk52WlJ' +
  '2VlZyUlpuSlpqRlpiQlpaOlZONlJCMko6KkIuJjoiHi4WGiIKEhYCCgn2Af3t+fHh7eXZ5dnR3dH' +
  'J0cXFyb29wbW5ubGxsamtraWppaWloaGhoaGdnZ2dnZ2dnaGhoaGhoaWlpampqa2tsbG1tbm5vcH' +
  'BxcXJzc3R1dnZ3eHl5ent8fH1+f4CAgYKDhISFhoeHiImKiouMjI2Ojo+PkJGRkpKTk5SUlJWVlZ' +
  'aWlpeXl5eXl5iYmJiYmJiYl5eXl5eXlpaWlpWVlZSUk5OTkpKRkZCPj46OjYyMi4uKiYmIh4aGhY' +
  'SEg4KBgYB/fn59fHx7enl5eHd3dnV1dHNzcnJxcHBvb25ubW1sbGxra2pqamlpaWloaGhoaGhoZ2' +
  'dnZ2dnZ2dnaGhoaGhoaWlpaWpqamtra2xsbW1tbm5vb3BwcXFyc3N0dHV2dnd3eHl5ent7fH19fn' +
  '9/gIGBgoODhIWFhoeHiIiJioqLi4yNjY6Oj4+QkJGRkZKSk5OTlJSUlZWVlpaWlpeXl5eXl5eYmJ' +
  'iYmJiYmJiYl5eXl5eXl5aWlpaWlZWVlJSUk5OTkpKSkZGQkJackoSCjZiWiH6DkJaNf3yGkY+CeX' +
  '2Jj4Z6doCKiHxzd4KHgHRxeYOCd25xe4B6bmtzfHtxaWt1enRqZm12dm1lZ3B1cGZjaXFyaWJkbH' +
  'FtZGFnbm9nYWJqb2tjYGZtbWdhYmlua2RhZm1uaGNkam9sZmRobm9rZmdtcW9qaGtxcm5qa3B0c2' +
  '5tcHV2c3BxdXh3dHN1eXp4dnZ6fXx6eXt+f358fX+CgYB/gYOEg4KDhYaGhoWGiImJiImKi4uLi4' +
  'yNjY2Ojo6Pj5CQkJGRkZKSkpOTk5OUlJSUlZWVlZaWlpaWlpeXl5eXl5eXl5eYmJiYmJiYmJiYmJ' +
  'iYl5eXl5eXl5eXl5aWlpaWlpWVlZWVlJSUlJOTk5OSkpKRkZGRkJCQj4+Pjo6OjY2MjIyLi4uKio' +
  'mJiYiIh4eHhoaFhYWEhIODg4KCgYGAgIB/f35+fn19fHx8e3t6enp5eXh4eHd3dnZ2dXV1dHR0c3' +
  'NzcnJycXFxcHBwb29vb25ubm5tbW1tbGxsbGtra2trampqampqaWlpaWlpaWloaGhoaGhoaGhoaG' +
  'hnZ2dnZ2dnZ2dnZ2dnZ2dnZ2doaGhoaGhoaGhoaGhoaWlpaWlpaWlqampqampra2tra2tsbGxsbG' +
  '1tbW1tbm5ubm5vb29vcHBwcHFxcXFycnJyc3Nzc3R0dHV1dXV2dnZ2d3d3eHh4eHl5eXp6enp7e3' +
  't8fHx8fX19fn5+fn9/f4CAhY1+dIGOhHZ+jol5fIyNfXqKkIJ6h5KIe4SSjH6BkJGCgI6Thn+LlY' +
  'uAiZWPg4aUk4aFkpaKhJCXjoWNmJKHi5eViomVmI2Jk5mRiZGalIuPmZeNjZeZkIyWm5ONk5uWjp' +
  'KamJCQmZqSkJeblJCWnJeQlJuZkpOampSSmZuWkpecmJOWm5mUlZqalZSZm5aUmJuYlJebmZWWmp' +
  'qWlZmal5WYmpiVl5qYlpeZmZaWmJmXlpiZl5aXmJiWl5iYl5eXl5eXl5eXl5eXl5eXl5aWlpaWlp' +
  'aWlpaWlpaWlpaWlpWVlZWVlZWVlZWVlZWVlZWUlJSUlJSUlJSUlJSUlJOTk5OTk5OTk5OTk5OSkp' +
  'KSkpKSkpKSkpKSkZGRkZGRkZGRkZGRkZCQkJCQkJCQkJCQkJCQj4+Pj4+Pj4+Pj4+Pj4+Ojo6Ojo' +
  '6Ojo6Ojo6Ojo2NjY2NjY2NjY2NjY2NjY2MjIyMjIyMjIyMjIyMjIyMjIyLi4uLi4uLi4uLi4uLi4' +
  'uLi4uLi4uKioqKioqKioqKioqKioqKioqKioqKioqKioqKiYmJiYmJiYmJiYmJiYmJiYmJiYmJiY' +
  'mJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiY' +
  'mJiYmJiYmJiYmJiYmJiYmJiYmJiYmJiYmKioqKioqKioqKioqKioqKioqKioqKioqKioqKi4uLl4' +
  'SBlo9+jZaDhJeNf5CVgoeXioCSk4GJl4mClJKBjJeHhJaQgo+WhoeXjoORlYWJl42Ek5SFjJeLhp' +
  'WThY6XioiWkYaQl4qKl5CHkpaJjJePiJSViY6XjoqVlIqQl46MlpOKkpeNjZeSi5SXjY+Yko2Vlo' +
  '2RmJGOlpWOk5iRj5eVj5SXkZGXlJCVl5GSmJSRlpeRlJiUkpeXkpWYlJOXlpOWmJSUmJaUlpiVlZ' +
  'iWlZeXlZaYlpaXl5aXmJeWl5eXl5eXl5eXl5eXl5eXl5eXl5iYmJiYmJiYmJiYmJiYmJiYmJiYmJ' +
  'iYmJiYmJiYmJiYmJeXl5eXl5eXl5eXl5eXl5eXl5eXl5eXlpaWlpaWlpaWlpaWlpWVlZWVlZWVlZ' +
  'WUlJSUlJSUlJOTk5OTk5OTkpKSkpKSkZGRkZGRkJCQkJCQj4+Pj4+Ojo6Ojo2NjY2NjIyMjIyLi4' +
  'uLioqKioqJiYmJiIiIiIeHh4eGhoaGhYWFhISEhIODg4OCgoKBgYGBgICAgH9/f35+fn59fX18fH' +
  'x8e3t7enp6enl5eXh4eHh3d3d2dnZ2dXV1dHR0dHNzc3NycnJycXFxcXBwcHBvb29vbm5ubm5tbW' +
  '1tbGxsbGxsa2tra2tqampqampqaWlpaWlpaWloaGhoaGhoaGhoaGhoZ2dnZ2dnZ2dnZ2dnZ2dnZ2' +
  'dnaGhoaGhoaGhoaGhoaWlpaWlpaWlqampqamtra2trbGxsbGx6ZWl6ZGx5Y3B5Y3N3ZHd2ZXp1Z3' +
  'xzaX5ybIBwb4FvcoFvdoFveYFvfYFwgIByg39zhX52h314iX17inx+i3yCjHyFjH2IjH6Li3+Ni4' +
  'GPioORioWTiYiUiYqViY2ViY+VipKVi5SVjJaUjZiUjpmTkJqTkpqTk5uSlZuSlpuSmJqTmZqTmp' +
  'mUm5iUm5eVm5eVm5aWm5WXmpWXmZSXmJSXl5OXlpOXlZOWlJKVk5KUkpGTkZGSkJCRj4+Pjo6OjY' +
  '2Mi4yLioqKiYmIiIeHhoaFhYSEg4OCgoGBgIB/f35+fX18fHt7enp5eXh4d3d2dnV1dHRzc3Jycn' +
  'FxcHBwb29ubm5tbW1sbGxra2tqampqaWlpaWloaGhoaGhoaGhnZ2dnZ2dnZ2dnZ2hoaGhoaGhoaW' +
  'lpaWlqampqa2trbGxsbW1tbm5vb29wcHFxcnJzc3R0dXV2dnd4eHl5enp7e3x9fX5+f4CAgYGCgo' +
  'OEhIWFhoaHh4iJiYqKi4uMjI2NjY6Oj4+QkJCRkZGSkpKTk5OUlJSUlJWVlZWVlZWWlpaWlpaWlp' +
  'aWlpaVlZWVlZWVlJSUlJOTk5OSkpKRkZGQkI+Pj46OjY2MjIuLioqJiYiIh4eGhYWEhIODgoGBgI' +
  'B/f359fXx8e3t6eXl4eHd3dnZ1dXR0c3NzcnJxcXFwcG9vb29ubm5tbW1tbWxsbGxsbGxsbGxsbG' +
  'xsbGxsbGxsbW1tbW1ubm5ub3Z8c2ZlcX17b2ZseoB4bGp1gYB0bHF/hX5ycHuGhntzeISKhHl3gY' +
  'uLgnp+iZCKgH6GkJGIgISOlI+Gg4uUlYyFiJKXk4qHjpeXj4mLlJmVjImPl5iRioyTmJSMiY+Vlo' +
  '+JipGVkouIjJKSjIeHjZGOh4SIjY2Ig4OIi4iCf4KHh4J+fYGEgn16fICAfHh4e358eHV3enp3dH' +
  'R2eHdzcXN1dXNxcHJ0c3BvcHJycW9vcHFxcG9wcXFwcHBxcXFxcXFycnJyc3NzdHR0dXV1dnZ3d3' +
  'd4eHl5enp7e3x8fX1+fn9/gICBgYKCg4OEhIWFhoaGh4eIiIiJiYmKioqLi4uLjIyMjIyMjY2NjY' +
  '2NjY2NjY2NjY2MjIyMjIyLi4uLioqKiYmJiIiIh4eGhoaFhYSEg4OCgoKBgYCAf39+fn19fXx8e3' +
  't6enp5eXh4eHd3d3d2dnZ2dXV1dXV0dHR0dHR0dHR0dHR0dHR0dHV1dXV1dnZ2dnd3d3d4eHh5eX' +
  'p6ent7e3x8fX1+fn5/f4CAgIGBgoKCg4OEhISFhYWGhoaHh4eHiIiIiIiJiYmJiYmJiYmJiYmJiY' +
  'mJiYmJiYmIiIiIiIeHh4eGhoaFhYWEhISDg4OCgoKBgYGAgH9/f35+fn19fXx8fHt7e3p6enp5eX' +
  'l5eXh4eHh4eHh4d3d3d3d3d3d3eHh4eHh4eHh5eXl5eXp6enp7e3t7fHx8fX19fX5+fn9/f4CAgI' +
  'GBgYKCgoKDg4ODhISEhISFhYWFhYWGhoaGhoaGhoaGhoaGhoaGhoaGhYWFhYWFhISEhISDg4ODg4' +
  'KCgoKBgYGBgICAf39/f35+fn5+fX19fXx8fHx8fHt7e3t7e3t7e3t7e3t7ent7e3t7e3t7e3t7e3' +
  't8fHx8fHx8fX19fX1+fn5+fn9/f39/gICAgICAgYGBgYGBgoKCgoKCgoKDg4ODg4ODg4ODg4ODg4' +
  'ODg4ODg4ODg4KCgoKCgoKCgoGBgYGBgYGAgICAgICAgH9/f39/f39/fn5+fn5+fn5+fn5+fn59fX' +
  '19fX19fX19fX1+fn5+fn5+fn5+fn5+fn5+fn9/f39/f39/f39/f3+AgICAgICAgICAgICAgICAgI' +
  'CAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICAgICA';

const SOURCES = {
  msg: MSG_URI, at: AT_URI, banner: BANNER_URI,
  // 场景音（r_09）：scene- 前缀分命名空间
  'scene-print': SCENE_PRINT_URI, 'scene-pour': SCENE_POUR_URI,
  'scene-crunch': SCENE_CRUNCH_URI, 'scene-sweep': SCENE_SWEEP_URI,
  'scene-ding': SCENE_DING_URI, 'scene-pop': SCENE_POP_URI,
  'scene-whisper': SCENE_WHISPER_URI, 'scene-binlap': SCENE_BINLAP_URI,
};
// 同类音冷却窗（ms）——场景音（r_09 定稿 §二）刻意比消息音长
const COOLDOWN = {
  msg: 2500, at: 1200, banner: 2000,
  'scene-pop': 1500, 'scene-ding': 2000, 'scene-print': 2500,
  'scene-pour': 3000, 'scene-binlap': 3000, 'scene-crunch': 4000,
  'scene-sweep': 5000, 'scene-whisper': 8000,
};
// 场景音音量系数（r_09 定稿 §二：比消息音再轻四成）
const SCENE_VOLUME = 0.6;
const els = new Map();   // kind → 复用的 Audio 元素（重叠时重头播即可）
const lastAt = new Map(); // kind → 上次发声时刻

/** 音量偏好（dh.ui.prefs 的 notifyVolume，0–100 整数、缺省 50）：设置卡
 *  的滑杆直写这里读，全部音色共用一个音量；钳到 [0,1] 防脏值。 */
export function volumePref() {
  const v = Number(prefs().notifyVolume);
  return Number.isFinite(v) ? Math.max(0, Math.min(1, v / 100)) : 0.5;
}

/** 播一枚提示音。偏好「提示音」关闭或环境不让播（自动播放策略、无
 *  音频栈）时静默让行——声音是点缀，绝不该报错或卡页。
 *  @param {'msg'|'at'|'banner'} kind
 *  @param {{force?:boolean}} [opts] force＝跳过冷却窗（设置卡试听用）
 *  @returns {boolean} 是否真的出声（冷却/偏好闸下为 false） */
export function sfx(kind, opts = {}) {
  const uri = SOURCES[kind];
  if (!uri) return false;
  if (prefs().notifySound === false) return false;
  const isScene = String(kind).startsWith('scene-');
  // 氛围音独立闸（dh.ui.prefs 的 sceneSound）：像素办公室的生活动静
  // （scene- 前缀九枚）自成一路——世界常转，人在对话/黑板板块时听得
  // 见动静却看不见画面（视觉全在办公室画布上），那一刻它只是噪音；
  // 关它不该陪葬消息叮咚。消息音（msg/at/banner）不走这道闸。
  if (isScene && prefs().sceneSound === false) return false;
  // 场景音深夜静音（r_09 §三：0–6 点全部场景音静默——深夜办公室就该
  // 安静；消息音不受限，那是通知不是氛围）
  if (isScene && !opts.force) {
    const h = new Date().getHours();
    if (h < 6) return false;
  }
  const now = Date.now();
  if (!opts.force && now - (lastAt.get(kind) || 0) < (COOLDOWN[kind] || 0)) return false;
  lastAt.set(kind, now);
  try {
    let el = els.get(kind);
    if (!el) { el = new Audio(uri); els.set(kind, el); }
    el.volume = volumePref() * (isScene ? SCENE_VOLUME : 1);
    el.currentTime = 0;
    const p = el.play();
    if (p?.catch) p.catch(() => {});
    return true;
  } catch { return false; }
}

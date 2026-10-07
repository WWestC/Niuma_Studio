# Homebrew Formula for Niuma Studio (牛马工作室).
#
# 本仓库不带自己的 tap。使用方法：在你的 GitHub 账号下建一个名为
# `homebrew-tap` 的公开仓库（WWestC/homebrew-tap），把本文件复制进去、
# 路径保持 Formula/niuma.rb，然后用户即可：
#
#   brew tap WWestC/tap https://github.com/WWestC/homebrew-tap
#   brew install niuma
#
# 每次发新版（推新 v* tag）后要同步更新两处 url 的版本号和两个 sha256：
#
#   curl -LO https://github.com/WWestC/Niuma_Studio/releases/download/vX.Y.Z/Niuma_Studio_darwin_arm64
#   curl -LO https://github.com/WWestC/Niuma_Studio/releases/download/vX.Y.Z/Niuma_Studio_darwin_amd64
#   shasum -a 256 Niuma_Studio_darwin_*
#
# NOTE: niuma 是 cgo 应用（原生 webview 窗口），`go install` 装不了——本
# Formula 刻意走 Releases 预编译二进制（由 .github/workflows/release.yml
# 的五平台矩阵产出），不从源码编译。
class Niuma < Formula
  desc "Pixel office where every employee is an AI agent"
  homepage "https://github.com/WWestC/Niuma_Studio"
  license "Apache-2.0"

  # TODO: 指向首个公开发布的 Release 后填真实版本与 sha256（见文件头说明）。
  # version 必须与 tag 名去 v 后一致（二进制经 -X 盖的是精确 tag 串，test 块按子串断言）。
  version "0.5"

  on_macos do
    on_arm do
      # TODO: sha256 待 Release 公开后用 shasum -a 256 算出填入
      url "https://github.com/WWestC/Niuma_Studio/releases/download/v0.5/Niuma_Studio_darwin_arm64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
    on_intel do
      # TODO: sha256 待 Release 公开后用 shasum -a 256 算出填入
      url "https://github.com/WWestC/Niuma_Studio/releases/download/v0.5/Niuma_Studio_darwin_amd64"
      sha256 "0000000000000000000000000000000000000000000000000000000000000000"
    end
  end

  def install
    # 每个 brew 环境只会下载当前架构的那一个产物，装成 niuma 命令。
    bin.install Dir["Niuma_Studio_darwin_*"].first => "niuma"
  end

  def caveats
    <<~EOS
      The installed binary is ad-hoc signed and not notarized. Homebrew
      downloads it without a quarantine attribute, so Gatekeeper stays out of
      the way; if you later move it around via a browser, see the README's
      Gatekeeper notes. The Dock icon and system notification banners need the
      .app bundle from a local ./build.sh instead.
    EOS
  end

  def test
    assert_match version.to_s, shell_output("#{bin}/niuma version")
  end
end

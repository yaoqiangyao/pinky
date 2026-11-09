<p align="center"><img src="assets/icon/pinky-macos-1024.png" width="112" alt=""></p>
<h1 align="center">Pinky</h1>
<p align="center"><b>macOS、Windows、Linux から、あなたが見守る中で、どんな AI エージェントにも iPhone や Android スマートフォンを使わせられます。</b></p>
<p align="center"><a href="https://github.com/DIYgod/pinky/releases/latest">ダウンロード</a> · <a href="#はじめに">はじめに</a> · <a href="#よくある質問">よくある質問</a> · <a href="https://discord.gg/S27b7SHjng">Discord</a></p>
<p align="center"><a href="README.md">English</a> · <a href="README.zh-CN.md">简体中文</a> · 日本語</p>
<p align="center"><sub>この README はコミュニティによる翻訳です。最新の内容は英語版の <a href="README.md">README.md</a> を参照してください。</sub></p>
<p align="center"><img src="assets/readme/demo.gif" width="760" alt="Claude Code がメッセージに返信を入力し、Pinky は許可されるまで送信のタップを保留し、そのあとでテキストが送られる"><br><sub>Android 16 エミュレーターでの、Claude Code の実際の実行です。Pinky はデフォルトで支払いの前に確認します。この実行では、メッセージの前にも確認するようにしています。</sub></p>

Pinky を使うと、どんなエージェント（Claude Code、Claude Desktop、Codex、Cursor、またはスクリプト）でも、MCP 経由で本物の iPhone や Android スマートフォンを操作できます。その間、あなたはコンピューターで様子を見て、危険な操作を承認し、操作を引き継ぎ、何が起きたかを振り返れます。承認は、エージェントへの指示ではなく、Pinky のゲートウェイが強制します。

- **すべてのプラットフォームに対応。** macOS（Apple silicon と Intel）、Windows、Linux 向けのアプリは 1 つです。Android スマートフォン（Android 10〜17）と、iOS 17.4 以降の iPhone を操作できます：iOS 27 以降では何もインストールする必要がなく、iOS 17.4〜26 では Xcode から一度だけヘルパーをインストールします。ヘルパーは iOS 27 でも選べます（iOS 27 とシミュレーターでは試していますが、27 未満の実機ではまだ試していません。[詳細](#スマートフォン)）。
- **小さくてネイティブ。** ウィンドウは [MyGo](https://github.com/egoist/mygo) が GPU で描画し、Electron も Web ビューも使いません：ダウンロードは 16〜22 MB で、Mac ではアイドル時のメモリが約 70 MB、CPU は 0% です。
- **どんなエージェントもワンクリック。** 「設定 › エージェント」で Claude Code、Claude Desktop、Codex、Cursor などを接続でき、MCP サーバーを使えるエージェントなら、同じコンピューターでも HTTP 経由でも動きます。
- **ロックされたスマートフォン？ コンピューターから解除できます。** スマートフォンの PIN を一度 Pinky に保存すれば、そのあとは解除のたびに Touch ID、Windows Hello、キーリングのパスワードで確認するだけです。エージェントが PIN を見ることはありません。
- **無料でオープンソース。** Apache-2.0 ライセンスで公開しており、アカウントを作る必要もありません。

<table>
<tr>
<td width="50%" valign="top"><img src="assets/readme/card-approval.jpg" width="100%" alt="何を待たせるかはあなたが決めます：Pinky のウィンドウは、「許可」をクリックするまでエージェントの送信のタップを保留します"><br><b>危険な操作はあなたが承認します。</b> 支払い、取引、送金は、あなたのクリックを待ちます。「設定 › 安全」では、送信、注文、削除、通話、インストール、アプリの外に出るリンク、設定の変更、あるいはすべての操作を待たせることもできます。Pinky はこれらのボタンを 10 の言語で見分けます。ルールは Pinky のゲートウェイで動くので、画面上の文章がエージェントをそそのかそうとしても、エージェントが自分で承認することはできません。</td>
<td width="50%" valign="top"><img src="assets/readme/card-off-limits.jpg" width="100%" alt="デフォルトで立ち入り禁止：エージェントがいつ確認するかと、ブロックしたアプリを示す「設定 › 安全」"><br><b>デフォルトで立ち入り禁止。</b> 銀行、ウォレット、パスワードマネージャーのアプリはエージェントから見えず、エージェントはパスワードやワンタイムコードの欄に入力できません。タスクで銀行やウォレットのアプリが必要になると、エージェントは確認を求め、あなたはそのタスクに限ってそのアプリを許可できます。新しいエージェントはどれもあなたの承認が必要で、あなたが与えた範囲しか使えません。</td>
</tr>
<tr>
<td width="50%" valign="top"><img src="assets/readme/card-second-screen.jpg" width="100%" alt="エージェント用の隠れた 2 つ目の画面：スマートフォン自身のホーム画面の横にあるエージェントの画面"><br><b>スマートフォンはあなたのもののままです。</b> Android 13 以降では、エージェントは隠れた 2 つ目の画面で作業するので、スマートフォン自身の画面はあなたが使えるまま、あるいは消灯してロックしたままにできます。それより古い Android スマートフォンや iPhone では、メインの画面を使います。</td>
<td width="50%" valign="top"><img src="assets/readme/card-take-over.jpg" width="100%" alt="いつでも引き継げます：あなたが操作している間、Claude Code は手を戻すまで一時停止しています"><br><b>いつでも引き継げます。</b> ミラーをクリックすると、手を戻すまでエージェントは一時停止します。Android では、自分でスマートフォンに触れても一時停止します。すべてを止めるのは、どこからでも ⌥⌘.（Windows と Linux では Ctrl+Alt+.）です。</td>
</tr>
<tr>
<td width="50%" valign="top"><img src="assets/readme/card-recording.jpg" width="100%" alt="すべての実行が記録に残ります：完了したタスクの手順と、その画面録画"><br><b>すべての実行が記録に残ります。</b> 各手順をわかりやすい言葉で、スクリーンショットと、手順ごとに印の付いた画面録画とともに残し、HTML のレポートとして書き出せます。すべてあなたのコンピューターにとどまります。</td>
<td width="50%" valign="top"><img src="assets/readme/card-iphone.jpg" width="100%" alt="iPhone でも：Pinky は「許可」をクリックするまで写真アプリの削除のタップを保留します"><br><b>iPhone でも。</b> 同じゲートウェイ、承認、記録が iOS 17.4 以降で使えます（<a href="#スマートフォン">何ができるか</a>）。ここでは iOS シミュレーターで、削除を確認するように設定しています。</td>
</tr>
</table>

**状態：** プレアルファです。Claude Code は Pinky を通して、本物のスマートフォン（USB で接続した Android スマートフォンと、iOS 27 の iPhone）を最初から最後まで使っており、エージェントは stdio か HTTP で接続できます。コンピューターからのロック解除、Android でのバックグラウンド作業、録画、書き出し、Wi-Fi でのペアリング、スマートフォンのファイルは作ってありますが、まだ人が試すのを待っているものもあります。今日何が動くかと、その試し方は [docs/ACCEPTANCE.md](docs/ACCEPTANCE.md) を参照してください。

## はじめに

Pinky をインストールし、スマートフォンを接続し、エージェントに Pinky を追加します。あとはエージェントにスマートフォンで何かをするよう頼み、Pinky のウィンドウでその様子を見るだけです。

### 必要なもの

| | 必要なもの |
| --- | --- |
| **Mac** | macOS 12 以降、Apple silicon または Intel |
| **Windows** | Windows 10 または 11、64 ビット（x64） |
| **Linux** | Ubuntu 20.04、Debian 11 またはそれより新しいシステム（glibc 2.29 以降）と GTK 3 の入った 64 ビットの PC（x86-64） |
| **Android スマートフォン** | Android 10〜17 と USB ケーブル。Pinky は自前の adb を持っています。Windows では USB ドライバーが必要なスマートフォンもあり、Linux では USB へのアクセスが必要です（[下記](#windows-と-linux-での-usb)） |
| **iPhone または iPad** | iOS 17.4 以降と、セットアップ用の USB ケーブル。そのあとは Wi-Fi でも使えます（[下記](#wi-fi-で使う-iphone-と-ipad)）。Pinky は Apple の開発者ディスクイメージを使って自分で準備します。このイメージは Xcode に付属していますが、頼めば Pinky がサードパーティのミラーからダウンロードすることもできます（[下記](#iphone)）。iOS 17.4〜26 では、iPhone にヘルパーアプリも必要です（[下記](#ios-17426-の-iphone)） |
| **エージェント** | Claude Code、Claude Desktop、Codex、Cursor など、MCP サーバーを使えるエージェント |

### 1. Pinky をダウンロードしてインストールする

[最新のリリース](https://github.com/DIYgod/pinky/releases/latest)から Pinky を入手します：

- **Mac：** Apple silicon（M1 以降）の Mac なら `pinky-<version>-arm64.dmg` を、Intel の Mac なら `pinky-<version>-amd64.dmg` をダウンロードします。開いて Pinky を「アプリケーション」にドラッグします。[Homebrew](https://brew.sh) があれば、`brew install --cask diygod/tap/pinky` でも同じことができます。
- **Windows：** `pinky-setup-<version>-amd64.exe` をダウンロードして実行します。管理者権限なしに、あなたのアカウントに Pinky をインストールします。インストーラーはまだ署名されていないので、SmartScreen が「Windows によって PC が保護されました」と表示することがあります：**詳細情報**を選んでから、**実行**を選んでください。インストールせずに Pinky を使うには、`pinky-<version>-windows-amd64.zip` を展開して `Pinky.exe` を開きます。
- **Linux：** Debian と Ubuntu では、`pinky_<version>_amd64.deb` をダウンロードし、`sudo apt install ./pinky_<version>_amd64.deb` でインストールします。それ以外のシステムでは、`pinky-<version>-linux-amd64.tar.gz` を専用のフォルダーに展開し、その中の `pinky` を実行します。

Pinky は新しいバージョンを自分で探し、Mac と Windows のインストーラー版では自分でインストールもします（[アップデート](#アップデート)）。

### 2. スマートフォンを接続する

Pinky を開いて **スマホを追加** を選びます。準備のできたスマートフォンは Pinky のウィンドウに表示されます。何かが足りないときは、Pinky のそのスマートフォンのページに何が足りないかが表示されます。

#### Android

**スマホを追加 › Android** には、スマートフォンのブランドに合わせた手順が、そのブランド独自のメニュー名や追加のスイッチとともに表示されます：Xiaomi、OPPO、vivo、HONOR、HUAWEI、Samsung、Pixel に対応し、それ以外は一般的な手順が表示されます。

1. スマートフォンで USB デバッグをオンにします：設定 › 端末情報で **ビルド番号** を 7 回タップしてから、設定 › システム › 開発者向けオプションで **USB デバッグ** をオンにします。ブランドによっては、これらのメニューの名前が少し違います。
2. スマートフォンをコンピューターにつなぎます。スマートフォンが USB デバッグを許可するか尋ねてきたら、**許可** をタップします。Android 11 以降では、代わりに **スマホを追加** から Wi-Fi でペアリングすることもできます（[失敗するとき](#wi-fi-でのペアリングに失敗するとき)）。Android 10 にはワイヤレスデバッグがありません：つないだあとで **Wi-Fi 接続に切り替え…** を選ぶと Wi-Fi に移せます（[下記](#スマートフォン)）。
3. Pinky は、スマートフォン用のアプリである Pinky Companion のインストールを提案します。これを入れると、画面の読み取りが何倍も速くなり、Pinky がどの言語でも入力できるようになります。スマートフォンがインストールの許可を一度求めることがあります。

各スマートフォンには、ミラーの下の ··· メニューに **チェック** があり、何が動いているかと、動いていないものの直し方（たとえば Xiaomi の USB デバッグ（セキュリティ設定）のスイッチ）を一覧します。

スマートフォンを持って出かけますか？ Tailscale があれば、Pinky は外出先でもスマートフォンに届きます（[方法](#外出先のスマートフォン)）。

<table>
<tr>
<td width="50%" valign="top"><img src="assets/readme/add-android.png" width="100%" alt="Xiaomi を選んだ「スマホを追加 › Android」：開発者向けオプションをオンにする場所と、3 つの USB デバッグのスイッチ"><br><sub>Xiaomi を選んだ <b>スマホを追加 › Android</b>。</sub></td>
<td width="50%" valign="top"><img src="assets/readme/checkup.png" width="100%" alt="Redmi の「チェック」：adb、接続、デバッグは合格。Pinky Companion とタップは修正が必要で、その手順付き"><br><sub>直すものが 2 つある Redmi の <b>チェック</b>。</sub></td>
</tr>
</table>

#### Android のツール

Pinky は、Android スマートフォンに接続するためのツールである adb を、Android のソースからビルドして自前で持っているので、ほかに何もインストールする必要はありません。コンピューターにすでに Android の platform tools（Android Studio、Homebrew、システムのパッケージのもの）がある場合、Pinky はそれを使い、その adb サーバーを Android Studio などのツールと共有します。

#### Windows と Linux での USB

- **Windows：** スマートフォンの USB デバッグにはドライバーが必要です。多くのスマートフォンでは Windows が自分でドライバーをインストールします。ドライバーがない場合、Pinky はそのスマートフォンをセットアップが必要なものとして一覧し、どのドライバーをインストールすればよいかを示します：Pixel なら [Google USB Driver](https://developer.android.com/studio/run/win-usb)、Samsung のスマートフォンなら [Samsung のもの](https://developer.samsung.com/android-usb-driver)、それ以外なら[スマートフォンのメーカーのもの](https://developer.android.com/studio/run/oem-usb)です。そのあと、スマートフォンを抜いてもう一度つないでください。
- **Linux：** コンピューターが Pinky にスマートフォンの USB デバイスを開かせる必要があり、それには Android スマートフォン用の udev ルールが要ります：Debian と Ubuntu では `sudo apt install android-sdk-platform-tools-common`、Arch では `android-udev`、Fedora では `android-tools` です。`plugdev` グループに入っている必要があることもあります。そのあと、スマートフォンをもう一度つなぎます。ルールが足りないときは Pinky がそう伝えます。

#### Wi-Fi でのペアリングに失敗するとき

ケーブルでは動くのに **Wi-Fi でペアリング** が終わらないとき、ペアリングのページは 30 秒以内に、Pinky に何が見えているかを伝えます：

- **スマホがまだネットワーク上に現れません：** ワイヤレスデバッグがオンになっていて、スマートフォンがコンピューターと同じ Wi-Fi につながっていることを確認してください。ゲスト用の Wi-Fi、AP（クライアント）分離のあるルーター、多くのオフィスのネットワークでは、デバイス同士がお互いを見つけられません。その場合は **ペアリングコードを使う** を選びます：スマートフォンで **ペア設定コードによるデバイスのペア設定** をタップし、表示される IP アドレスとポート、そして 6 桁のコードを入力します。これらはダイアログを開くたびに変わります。そうすれば Pinky はペアリングのために探す必要がなくなります。そのあとでもスマートフォンが見つからない場合、Pinky はワイヤレスデバッグの画面の上部にある IP アドレスとポート（ペアリングのダイアログのものではありません）を尋ね、そこに接続します。そのアドレスでもスマートフォンに届かない場合は、ネットワークがデバイス同士を分けています：家庭のネットワークのゲスト用ではなく本体のネットワークのように、お互いに届く Wi-Fi を使うか、ケーブルを使ってください。
- **Pinky にこのネットワーク上でスマホを探す権限がありません：** Mac では、Pinky にローカルネットワークへのアクセスが必要で、それがなくても macOS はエラーを出しません：検索で何も見つからないだけです。**「ローカルネットワーク」設定を開く** を選び、システム設定 › プライバシーとセキュリティ › ローカルネットワークで Pinky をオンにして、もう一度試してください（Pinky がローカルネットワーク上のデバイスを探してもよいかと macOS に尋ねられたら、**許可** をクリックします）。Mac では、スマートフォンの **チェック** の **ローカルネットワーク** にもこれが表示されます。Windows と Linux では、ファイアウォールが同じように検索をブロックすることがあります：Pinky にローカルネットワークを使わせるか、コードでペアリングしてください。
- **Pinky はこのネットワーク上でスマホを探せません：** コンピューターが Wi-Fi か Ethernet につながっていることを確認してください。
- **スマホは見つかりましたが、ペアリングが終わっていません：** スマートフォンにエラーが表示されているか、ワイヤレスデバッグの画面に戻っている場合は、コードをもう一度読み取るか、コードでペアリングしてください。

ペアリングがエラーで止まったときは、adb が何を報告したかがページに表示されます：

- **ペアリングが終わる前に、スマホがペアリングを終了しました**（QR コード）：スマートフォンが読み取りの画面を離れたか、Wi-Fi を変えたか、スマートフォンとコンピューターの間の何かが接続を切りました。adb にはどれなのかわかりません。スマートフォンを読み取りの画面にしたままもう一度試すか、メッセージの横の **ペアリングコードを使う** を選んでください。
- **スマホがペアリングを受け付けませんでした**（ペア設定コード）：コードが間違っているか、ポートがペアリングのダイアログのものではありません。ワイヤレスデバッグの画面の上部にあるアドレスは接続用で、ペアリング用ではありません。そして adb はどちらの間違いにも同じように答えます。
- **そのアドレスにはペアリングを待っている端末がありません** または **スマホがペアリングの待機をやめました：** ペアリングのダイアログか読み取りの画面が閉じたか、ポートが間違っています。もう一度開いて、今表示されているものを使ってください。
- **そのアドレスのスマホに届きません** または **Pinky が接続できませんでした：** 上記のとおり、ネットワークがコンピューターとスマートフォンを分けています。コンピューターの VPN やファイアウォールが原因のこともあります。
- **このパソコンの adb サービスが古く、Wi-Fi でのペアリングができません：** 別のプログラムが platform-tools 30 より古い adb を起動しました。そのプログラムを終了するか更新すれば、Pinky が自分の adb を起動します。

Pinky は adb 自身の言葉をログに書き込み、**ヘルプ › フィードバックを送信…** はそれを診断情報とともに添付します。

Wi-Fi でのペアリングには Android 11 以降が必要です。Android 10 のスマートフォンは、代わりにケーブルから Wi-Fi に移します（**Wi-Fi 接続に切り替え…**、[下記](#スマートフォン)）。Android 16 までは、スマートフォンが Wi-Fi のネットワークを離れると（同じネットワークの別のアクセスポイントに移った場合も）、あるいは再起動すると、ワイヤレスデバッグは自動でオフになります：もう一度オンにするか、**スマホを追加** からもう一度ペアリングしてください。

#### iPhone

Pinky は iPhone を自分で準備します。**スマホを追加 › iPhone** が手順を案内します。

1. iPhone をコンピューターにつなぎ、ロックを解除して **信頼** をタップし、パスコードを入力します。Windows では、先に Microsoft Store から Apple Devices（または iTunes）をインストールし、Linux では `usbmuxd` パッケージをインストールします。足りないときは「スマホを追加」がそう伝えます。
2. iPhone で、設定 › プライバシーとセキュリティの **デベロッパモード** をオンにします：Pinky がそこにスイッチを表示させます。iPhone は再起動するので、ロックを解除して **オンにする** をタップし、パスコードを入力します。
3. Pinky が iPhone に Apple の開発者ディスクイメージをマウントし、準備完了として一覧します。iOS 27 の iPhone は再起動してもイメージを保持します。iOS 27 未満では、再起動のたびに、iPhone のロックを解除したあとで Pinky がもう一度マウントします。

開発者ディスクイメージは、Apple が開発者向けに無料で提供しているアプリ [Xcode](https://apps.apple.com/app/xcode/id497799835) に付属しており、Pinky には同梱していません。Mac では、Xcode を一度インストールして開いてください：Pinky は Xcode がインストールしたイメージを使い、そのあとで Xcode を起動する必要はありません。iOS 27 以降の iPhone には Xcode 27 以降のイメージが必要です：Apple の署名サーバーは古いイメージにもう署名しないためで、そのときは「スマホを追加」が、Pinky がどのイメージを使ったかと、新しいものの入手方法を伝えます。Windows と Linux では、そのような Mac から `/Library/Developer/DeveloperDiskImages/iOS_DDI` を Pinky のフォルダーにコピーします（[場所](docs/notes/ios.md#preparing-a-phone-without-xcode)）。どちらもない場合、「スマホを追加」は、サードパーティのミラーである github.com/doronz88/DeveloperDiskImage（Xcode 27.0 beta 4、32 MB。Apple ではなく pymobiledevice3 の作者が管理しています）からイメージをダウンロードすることを、「ダウンロード」をクリックしたときに限り提案します。Pinky はすべてのファイルを、期待するチェックサムと照合します。iOS 27 以降では、iPhone にアプリは何もインストールされません。

iPhone 13 mini（iOS 27.0.1）で確認したこと：Pinky が Xcode のイメージを自分でマウントすること（イメージを外したあとで約 1.5 秒）、スマートフォンにすでにあるイメージを保持すること、iPhone が再起動してもイメージを保持すること。まだ実機で試していないこと：ダウンロードしたイメージ、デベロッパモードのスイッチ、iOS 27 未満の iPhone、Windows や Linux での iPhone。

#### Wi-Fi で使う iPhone と iPad

Pinky は、iPhone や iPad がコンピューターと同じ Wi-Fi にあり、コンピューターがそこにそれを一覧しているとき、Wi-Fi でも使います。コンピューターが一覧するのは、そのスマートフォンで Wi-Fi 接続がオンになってからです。スマートフォンをつないでコンピューターを信頼すると、Pinky がそれをオンにします：これは Finder の **Wi-FiがオンになっているときにこのiPhoneを表示** と同じ設定なので、すでにそう設定してあるスマートフォンはそのままです。デフォルトでオンで、**設定 › スマホ › Wi-Fi で iPhone と iPad を使う** でオフにできますが、オフにしてもスマートフォンの設定はそのままです（Finder でオフにします）。

- Pinky が使ったことのある iPhone や iPad は、抜いても、同じ設定の同じスマートフォンとして動き続けます：Pinky が自分で Wi-Fi に移し、つなぐとケーブルに戻すので、エージェントのタスクは続きます。その間、呼び出しは数秒待ちます。
- 家族がこのコンピューターとペアリングした iPad のように、Pinky が使ったことのないものは、**Wi-Fi で検出** と表示されます。Pinky はその名前、モデル、iOS のバージョンを読むだけで、ほかには何もせず、エージェントにも見えません。**使用** を選ぶと Wi-Fi でセットアップし、**無視** を選ぶと、つなぐまで一覧から外します。
- Wi-Fi 上でロックされている iPhone や iPad は **ロック中（Wi-Fi）** と表示され、ロックを解除すると Pinky が接続します：スリープ中は 1 分ほどおきにネットワークから外れ、そのときに接続すると画面が点灯するためです。
- Wi-Fi でセットアップする場合：コンピューターを信頼するにはケーブルが必要で、隠れているデベロッパモードのスイッチを表示させるのにもケーブルが必要です（Xcode で使ったことのあるスマートフォンでは、すでに表示されています）。デベロッパモードをオンにするのはスマートフォンの上で行い、Pinky は開発者ディスクイメージを Wi-Fi でもマウントしますが、1.5 秒ではなく約 9 秒かかります。どの手順にケーブルが必要かは **スマホを追加 › iPhone** が伝えます。

Wi-Fi では、どの手順もケーブルより遅く、特にスクリーンショットが遅くなります。iPad Pro 11 インチ（第 3 世代、iPadOS 27.0.1）で、Wi-Fi とケーブル（かっこ内）を比べて計測しました：スワイプが画面に表示されるまで 80〜210 ms（60〜90 ms）、ホーム画面の読み取りが 0.6〜2.6 秒（0.5〜0.6 秒）、スクリーンショットが 5 秒（1 秒）、映像のパケットの損失が 0.2 %（なし）でした。抜くと実行中のタスクは約 5 秒で Wi-Fi に移り、つなぐと 2〜6 秒でケーブルに戻りました（[詳細](docs/notes/ios.md#wi-fi)）。

Mac では、システムのデバイスサービスである usbmuxd が、一度接続の切れた（たとえば Wi-Fi をオフにしてオンにした）Wi-Fi 上の iPhone を見失い、最大で 1 時間ほど探し直さないことがあります。Finder も同じように見失います。Pinky は、使っているスマートフォンがネットワーク上にあるのに一覧からなくなったことに気づくと、それを **Wi-Fi で見失いました** と表示し、そのページで **デバイスサービスを再起動…** を提案します：確認すると macOS が管理者のパスワードを求め、ケーブルでつないだものも含めて、すべての iPhone と iPad が 1〜2 秒切断されます。ターミナルで `sudo killall usbmuxd` を実行しても同じことができます。

Pinky がスマートフォンを開いている間や、ケーブルと Wi-Fi の間で移している間は、オフラインではなく **接続中** や **Wi-Fi に切り替え中** と表示します。Wi-Fi では、そこで約 5 秒かかるスクリーンショットではなく、ライブの映像から文字を読み取ります。パスコードのない iPhone や iPad はロックされているとは判断されないので、スリープのためにネットワークから外れたあとは、戻ってから 30 秒たって初めて Pinky が接続します。

#### iOS 17.4〜26 の iPhone

iOS 27 未満では、iPhone はスマートフォン上のヘルパーアプリを通してしか、コンピューターからのタップや入力を受け付けません：それが WebDriverAgent で、Apple ID を使って Xcode から一度ビルドしてインストールします（[方法](docs/notes/ios.md#installing-the-webdriveragent-helper)）。無料の Apple ID では 7 日ごとにインストールし直す必要があります：その 2 日前になると、スマートフォンのページと「チェック」が知らせてくれます。期限が切れると、スマートフォンはセットアップが必要と表示され、その理由が示されます（[インストールし直す方法](docs/notes/ios.md#when-the-helpers-signature-runs-out)）。iOS 27 未満の iPhone ではまだ試していません。ヘルパーがないと、Pinky にできるのは画面の表示と読み取り（Mac のみ）とアプリを開くことだけで、タップ、スワイプ、入力はできません。iOS 17.3 以前には対応していません。

### 3. エージェントに Pinky を追加する

Pinky で **設定 › エージェント** を開き、使っているエージェントの横の **接続** をクリックします。Pinky は、コンピューター上の Claude Code、Claude Desktop、Codex、Cursor、VS Code、Copilot CLI、Windsurf（Devin Desktop）、Gemini CLI を見つけ、アプリ自身のパスでエージェントの MCP サーバーに自分を追加します：`claude mcp add` のようにエージェント独自のコマンドがあればそれを使い、なければ設定ファイルに書き込みます。設定ファイルは先にコピーし、Pinky の項目だけを変更します。また、エージェントがスキルを読む場所に、Pinky のエージェント用ガイド [SKILL.md](docs/SKILL.md) もインストールします。エージェントが接続されるまでは、Pinky のサイドバーのカードが接続を提案するか、エージェントの入手先を伝えます。

<p align="center"><img src="assets/readme/agents.png" width="620" alt="設定 › エージェント › エージェントを接続：Claude Code は接続済みで、Claude Desktop、Codex、Cursor、VS Code には「接続」ボタンがある"></p>

- **Claude Desktop：** 「接続」を押すと、Claude Desktop で Pinky の拡張機能が開きます。そこで **Install** をクリックしてください。[リリースのページ](https://github.com/DIYgod/pinky/releases/latest)の `pinky-<version>.mcpb` をダブルクリックしても同じです。
- **その他のエージェント：** **設定 › エージェント › エージェントを手動で追加** に、あなたの Pinky 用のコマンドが、それをコピーするボタンとともに表示されます。それを実行する MCP サーバー（stdio）を追加してください。Homebrew の cask か Linux のパッケージを使っている場合、JSON の設定では次のようになります：

  ```json
  { "mcpServers": { "pinky": { "command": "pinkyctl", "args": ["mcp"] } } }
  ```

  ターミナルの Claude Code なら `claude mcp add --scope user pinky -- pinkyctl mcp` です。ほとんどのエージェントはガイドを `~/.agents/skills/pinky/SKILL.md` から、Claude Code は `~/.claude/skills/pinky/SKILL.md` から読みます。

コマンドは `pinky` ではなく `pinkyctl` です：多くのコンピューターには、すでに `pinky` という名前の別のプログラムがあるためです。別のコンピューターやコンテナーの中のエージェントは、代わりに HTTP で接続できます（[方法](docs/mcp.md#over-http)）。「接続」がエージェントごとに何を変更するかは [docs/mcp.md](docs/mcp.md#connecting-an-agent) に書いてあります。

### 4. 試してみる

エージェントにスマートフォンで何かをするよう頼んでみてください。たとえば「スマートフォンでメッセージを開いて、Sam に遅れると伝えて」です。エージェントが初めて接続すると、Pinky はそれを許可するかどうかと、何をしてよいかを尋ねます。支払いは Pinky のウィンドウであなたのクリックを待ちます。「設定 › 安全」では、送信や削除なども待たせられます。

手元にスマートフォンがありませんか？ Pinky を終了し、スクリプトで動くデモ用のスマートフォン 2 台（承認が必要な支払い、ブロックされた銀行アプリ、パスワードの欄）付きで起動してください：Mac では `/Applications/Pinky.app/Contents/MacOS/Pinky --fake`、Linux では `pinkyctl --fake`、Windows の PowerShell では `& "$env:LOCALAPPDATA\Programs\Pinky\Pinky.exe" --fake` です。

## Pinky の使い方

### スマートフォン

- **Android：** USB か Wi-Fi で接続します（[上記](#android)）。Pinky は adb と、同梱の scrcpy サーバーを通して動きます。Pinky に同梱されているコンパニオンアプリは、画面を 2 秒ではなく約 70 ms で読み取り、クリップボードを使わずにどの言語でも入力します：スマートフォンのページがインストールを提案します（スマートフォンが一度許可を求めることがあります）。そのユーザー補助サービスがオンになるのは、エージェントがスマートフォンで作業している間だけです。銀行アプリは、ユーザー補助サービスが動いていると起動を拒むためです（[理由](#よくある質問)）。Android 13 以降では、エージェントはバックグラウンドのディスプレイで作業するので、スマートフォン自身の画面は消灯してロックしたままにできます。アプリがユーザー補助のツリーに説明せずに描画する文字（スーパーアプリの中のミニプログラムなどの Web ページ、ゲーム、地図など）は、macOS と Windows では画面のピクセルから読み取る（OCR）ので、エージェントはそれを読んでタップできます。ブロックされたアプリをこの方法で読むのは、あなたがそのタスクで許可した場合だけで、ロックされたスマートフォンを読むことはなく、画像も残しません。
- **Android 10 での Wi-Fi：** Android 10 にはワイヤレスデバッグがありません。スマートフォンをつなぎ、映像の下の ⋯ メニュー（または「設定 › スマホ」）で **Wi-Fi 接続に切り替え…** を選んでください：Pinky はスマートフォンの adb をネットワークで待ち受けさせ（`adb tcpip`、ポート 5555）、その Wi-Fi のアドレスで接続し、抜いてよくなったら知らせます。スマートフォンがネットワークに戻ると、アドレスが変わっていても、自分でまた接続します。これがオンの間は、同じネットワーク上のどのコンピューターでもスマートフォンへの接続を試せます：自分で接続していないときにスマートフォンが **USB デバッグを許可しますか？** と尋ねてきたら、**キャンセル** をタップしてください。信頼できる Wi-Fi でだけ使ってください。これはスマートフォンが再起動するまで続きます。再起動したら、一度つないで **Wi-Fi 接続を再開** を選んでください。同じメニューの **Wi-Fi 接続を停止** でオフにできます（[詳細](docs/away-from-home.md#wi-fi-without-wireless-debugging)）。
- **iPhone：** USB か Wi-Fi で接続し（[上記](#wi-fi-で使う-iphone-と-ipad)）、Pinky が準備します（[上記](#iphone)）。iPad も同じように動きます。Pinky に何ができるかは、iOS のバージョンと Pinky の接続のしかたで決まり、接続のしかたは「設定 › スマホ」で iPhone ごとに選べます（「自動」は、下のようにバージョンで選びます）：

  | iOS | 方法 | 何ができるか | テストした環境 |
  | --- | --- | --- | --- |
  | 27 以降 | Xcode の Device Hub プロトコル。スマートフォンには何もインストールしない | すべて。入力はテキストを貼り付けて、クリップボードを元に戻します | iPhone 13 mini、iOS 27.0.1 |
  | 17.4 以降、ヘルパーあり | Xcode から一度インストールする WebDriverAgent ヘルパー（[方法](docs/notes/ios.md#installing-the-webdriveragent-helper)）：iOS 17.4〜26 では自動、27 では選択可能 | すべて。スマートフォンは画面を共有しないので、画面共有を拒む銀行アプリも動き続けます（テストでは DBS digibank と招商銀行）。Device Hub より遅く、ホーム画面の読み取りは 0.5 秒未満ではなく約 7 秒、タップは 0.5〜0.8 秒かかります。ヘルパーが起動する前に、iOS がスマートフォンでパスコードを求めることがときどきあります。ライブの映像には macOS が必要です | iPhone 13 mini、iOS 27.0.1、ヘルパーを選択。iOS 18.5 と 26.5 のシミュレーター。iOS 27 未満の実機ではまだ |
  | 17.4〜26、ヘルパーなし | 開発者サービスのみ。何もインストールしない | 見ること（毎秒数フレーム、macOS のみ）、画面の読み取り（OCR で位置を決定、macOS のみ）、アプリを開くこと。タップ、スワイプ、入力、ホームはできません：ヘルパーがないと、スマートフォンは入力を受け付けません | iOS 27.0.1 で同じサービス。iOS 27 未満の実機ではまだ |
  | 17.3 以前 | | 対応していません：17.4 より前は接続に管理者権限が必要で、iOS 16 以前には、現在の Xcode にはもう含まれていない開発者ディスクイメージが必要です | |

  ヘルパーは Appium の WebDriverAgent で、あなた自身の Apple ID で署名します。署名の期限は、無料の Apple ID では 7 日、有料の開発者アカウントでは 1 年です。Pinky は期限が切れる時期を iPhone の上で読み取ります：その 2 日前になると、スマートフォンのページと「チェック」が、Xcode からヘルパーをインストールし直すよう知らせます。期限が切れると、ヘルパーの起動に失敗するのではなく、スマートフォンはセットアップが必要と表示され、その理由が示されます（[方法](docs/notes/ios.md#when-the-helpers-signature-runs-out)）。
- **iPhone での画面共有：** Pinky が iPhone の画面をストリーミングしている間、iOS はアプリに画面が共有されていることを伝え、一部の銀行アプリ（DBS digibank や招商銀行など）は起動を拒むか警告します。Pinky がストリーミングするのは、Pinky でスマートフォンのページかタイルが開いている間、エージェントがそのスマートフォンで作業している間、そしてそのあとの 30 秒間です。それが終われば、そのようなアプリはまた動きます。CoreDevice 経由では、エージェントはそれらのアプリを使えません。代わりに、その iPhone を WebDriverAgent ヘルパー経由で接続してください（「設定 › スマホ」）：そうするとスマートフォンは画面を共有しないので、エージェントもそれらのアプリを使えます。
- **ロックされたスマートフォン：** Pinky がスマートフォンを起こしたままにすることはありません。手順の前に画面を点灯します。スマートフォンの PIN を「設定 › スマホ」に保存すると、Touch ID でコンピューターからロックを解除できます。エージェントが PIN を見ることはありません。Android では、AOSP 風のロック画面（Mi 10 Pro の LineageOS、Android 10、16、17 のエミュレーター）で確認しています。Xiaomi、Samsung、OPPO、OnePlus、vivo、Honor、Huawei のロック画面は、それらについてわかっていることから作ったテストでしかカバーしておらず、Pinky が PIN パッドを確信をもって読み取れない場合は何も入力しません（[詳細](docs/notes/integration.md#unlocking-other-android-phones)）。
- **ファイル：** Android では、エージェントはスマートフォンの Pinky フォルダー（`/sdcard/Pinky`。スマートフォン上でエージェントが届く唯一の場所です）とコンピューター上のフォルダーの間でファイルをコピーします。スマートフォンへのコピーは先にあなたに確認します。そこにコピーした画像はスマートフォンのギャラリーや写真の選択画面に表示されるので、エージェントはそれをチャットアプリで添付できます。別のコンピューターのエージェントはそのフォルダーに届かないので、まず Pinky にファイルを送ります：小さなファイルは呼び出しの中で、大きなファイルは Pinky が渡す 1 回限りのリンクに送ります。そのあと、ファイルをスマートフォンにコピーするときは、いつもどおりあなたに確認します（[詳細](docs/mcp.md#uploading-a-file)）。iPhone では使えません。
- **スマートフォンの削除：** スマートフォンの ⋯ メニュー（オフラインのときはそのページ、「すべてのスマホ」のタイル、サイドバーの右クリックメニュー、「設定 › スマホ」にもあります）の **このスマホを削除…** で、スマートフォンを一覧から外し、保存した PIN と設定を削除します。そのタスク、スクリーンショット、録画は、それらも削除するチェックを入れない限り「アクティビティ」に残り、そのスマートフォンで実行中のタスクは止まります。ケーブルでつないだスマートフォンは、抜くまで、新しいスマートフォンとしてすぐに戻ってきます。Wi-Fi のスマートフォンは切断され、adb が自分でまた接続しても一覧から外れたままです。追加し直すには、「スマホを追加」でもう一度ペアリングするか、つないでください。Wi-Fi の iPhone や iPad は、つなぐまで一覧から外れたままです。Pinky はスマートフォンとコンピューターのペアリングを解除しません：USB デバッグやワイヤレスデバッグのペアリング、あるいは iPhone の信頼は、ほかのアプリのために残ります（[詳細](docs/notes/gateway.md#removing-a-phone)）。

### 外出先のスマートフォン

Android スマートフォンは、コンピューターのネットワークにある必要はありません。コンピューターとスマートフォンの両方に [Tailscale](https://tailscale.com) を入れ、同じアカウントでサインインしておけば、Pinky はそれを通してスマートフォンに届きます：コンピューターを家に置いたまま、スマートフォンを持ってモバイルデータや別の Wi-Fi で出かけられます。Android は同時に 1 つの VPN しか動かせないので、スマートフォンでは他の VPN やプロキシのアプリをオフにしておいてください：それらは Tailscale をオフにしてしまいます。

1. スマートフォンがコンピューターの近くにある間に、ケーブルか同じ Wi-Fi で一度だけ設定します：**設定 › スマホ › 外出先で使う › オンにする…**。**スマホを追加 › Android › 別のネットワークにあるスマホは Tailscale で接続** が、両方の側での Tailscale の設定を案内し、tailnet 上のスマートフォンを一覧します。
2. Pinky はスマートフォンの adb をネットワークで待ち受けさせ（`adb tcpip`、ポート 5555）、その Tailscale のアドレスでスマートフォンに接続します。スマートフォンがコンピューターのネットワークで接続されていないときはいつもその接続を使うので、出かけてもスマートフォンは一覧に残ります。
3. そのあと、スマートフォンの adb は、スマートフォンが参加するどのネットワークでも、すでに信頼しているコンピューターからの接続を受け付けます。それ以外のコンピューターでは、スマートフォンが確認を求めます。暗号化されているのは Tailscale を通る経路だけです。これはスマートフォンが再起動するまで続き、**オフにする** ですぐに止まります。Pinky は Tailscale の状態を読むだけで、その設定を変えることはありません。
4. **外出中のスマホのためにパソコンをスリープさせない**（「設定 › スマホ」、デフォルトでオン）は、スマートフォンが Tailscale 経由で接続されている間、コンピューターのアイドル時のスリープを止めます。MacBook はふたを閉じればスリープします。

家では Tailscale は Wi-Fi と同じくらい速く、外出先ではスマートフォンのアップロードが映像を運ぶので、Pinky は Tailscale 経由では軽めにストリーミングします（1 Mbit/s）。弱い 4G のテザリングでは、各手順に数秒かかります。詳細、計測結果、データの使用量、接続できないときの対処は [docs/away-from-home.md](docs/away-from-home.md) にあります。エージェントもコンピューターから離れた場所に置けます：[リモートのエージェント](docs/mcp.md#over-http)。

### アップデート

Pinky は起動時と数時間ごとに新しいバージョンを探し、変更点と **今すぐ更新**、**あとで** を載せたカードを表示します。「設定 › 詳細」の **自動更新**（デフォルトでオン）にしておくと、どのエージェントも作業しておらず、誰も 10 分間 Pinky を使っていないときに自分でインストールし、手の空いている Android スマートフォンの Pinky Companion も更新します。そのユーザー補助サービスは次のタスクまでオフのままです。2026 年 11 月 8 日までのビルドが使っていた公開鍵で署名された Pinky Companion は、アンインストールされてから、Pinky の秘密鍵で署名されたものとしてインストールし直され、Pinky がそのことを伝えます。USB 経由でアプリをインストールする前に確認するスマートフォンでは、スマートフォンのページがその理由を伝え、あなたが **更新** を押すのを待ちます。アップデートのあとは、**新機能** に、それまで使っていたバージョン以降の変更が一覧されます（[CHANGELOG.md](CHANGELOG.md) から。Pinky が中国語のときは、その中国語版の [CHANGELOG.zh-CN.md](CHANGELOG.zh-CN.md) から）。

- **macOS：** Pinky.app は、ダウンロードのチェックサム、Pinky の開発者が署名していること、Apple が公証したことを確認してから自分を置き換え、新しいバージョンが起動しなければ古いバージョンに戻ります。Homebrew の cask のコピーも同じように自分で更新し、`brew upgrade` は最新のあいだは手を付けません。ディスクイメージから実行しているコピーや、書き込めないフォルダーから実行しているコピーは、代わりに更新の方法を伝えます。
- **Windows：** インストーラーでインストールした Pinky は、新しいインストーラーのチェックサムを確認してから、それを静かに実行して新しいバージョンを起動し、それが起動しなければ古いバージョンに戻ります。ポータブル版のコピーは、インストーラーをダウンロードしてくれます。
- **Linux：** Pinky はリリースをダウンロードし、あなたがインストールします。
- **ターミナル：** `pinkyctl update` は、Pinky が動いていない間にアップデートをインストールします。`pinkyctl daemon` は、アップデートが出ていることをログに書くだけです。

詳細：[docs/notes/updates.md](docs/notes/updates.md)。

### 利用統計、クラッシュレポート、フィードバック

Pinky は匿名の利用統計（実行したタスク、呼び出したツール、接続したスマートフォンのような数で、1 日に 1 回ほど、ランダムなインストール id とともに）とクラッシュレポートを送ります。どちらも「設定 › プライバシー」か `DO_NOT_TRACK=1` でオフにできます。画面、入力したテキスト、アプリ名、スマートフォンの名前と id、PIN、ファイル名が送られることはありません。「ヘルプ › フィードバックを送信…」は、あなたのメッセージと診断情報を書き込んだ GitHub の issue を開くので、あなたが自分で読んでから送信します。すべての項目は [docs/TELEMETRY.md](docs/TELEMETRY.md) に一覧しています。

## よくある質問

<details>
<summary><b>Pinky で使えるのはどのエージェントですか？</b></summary>

MCP サーバーを使えるエージェントなら、どれでも使えます：stdio では `pinkyctl mcp` を実行し、HTTP ではエージェントのトークンを使います（[方法](docs/mcp.md#over-http)）。**設定 › エージェント** は、コンピューター上で見つけた Claude Code、Claude Desktop、Codex、Cursor などのエージェントをワンクリックで接続します。これまでに本物のスマートフォンを最初から最後まで使ったのは Claude Code です。Pinky は独自のモデルを持っておらず、考えるのはあなたのエージェントです。

</details>

<details>
<summary><b>エージェントが危険な操作を自分で承認したり、そそのかされて承認したりすることはありますか？</b></summary>

ありません。ルールは、エージェントとスマートフォンの間にある Pinky のゲートウェイで動きます。あなたの確認が必要な操作は、`approval_pending` としてエージェントに返され、あなたが Pinky で **許可** をクリックするまで、スマートフォンでは何も起きません。エージェントには、それを確定するために送れるものが何もありません。実行する前に、Pinky はボタンがまだ同じ場所にあることを確認し、60 秒以内に応答がなければ「いいえ」とみなします。Pinky はこれらのボタンを 10 の言語のラベルで見つけるので、珍しいボタンはすり抜けることがあります：もっと細かく管理したい場合、「設定 › 安全」には **すべての操作を確認** と **読み取り専用** があります。

</details>

<details>
<summary><b>エージェントに見えないものは何ですか？</b></summary>

銀行、ウォレット、パスワードマネージャーのアプリです。これは Pinky に付属する一覧で、「設定 › 安全」で追加できます：エージェントはそれらから画面を受け取れず、その通知も隠されます。エージェントはパスワードやワンタイムコードの欄に入力できません。タスクで銀行やウォレットのアプリが必要になると、Pinky がまずあなたに確認します。許可すると、エージェントはタスクの残りの間、そのアプリを開いて読めます。そこでもあなたのルールが確認を求めるものは引き続き確認を求め、Pinky はそのスクリーンショットや録画を残しません：ピクセルにしか表れない文字を読むために、Pinky はその画面をメモリの中で見て、エージェントには画像ではなく文字だけを渡します。パスワードマネージャーが提案されることはありません。

ワンタイムコードは、エージェントが読む前に伏せられます：テキストメッセージ、メール、通知、あるいは画面のどこにあっても、「Your code is 482913」はエージェントには「Your code is ••••••」として届き、エージェントが受け取るスクリーンショットと 「アクティビティ」のスクリーンショットでは、コードが灰色の箱で覆われます。価格、電話番号、日付、そしてコードと関係のない文章の中の数字はそのままです。画面録画はコンピューターにとどまり、エージェントには決して渡らないので、コードがそのまま映っています。これは「設定 › 安全 › エージェントからコードを隠す」でオフにできます。

</details>

<details>
<summary><b>スマートフォンの画面はクラウドに送られますか？</b></summary>

Pinky を通しては送られません。スクリーンショット、録画、履歴はコンピューターに 30 日間とどまります（「設定 › プライバシー」で変更でき、そこで削除もできます）。エージェントが読んだものは、あなたが選んだエージェントに送られます。これは、エージェントに渡すどのツールでも同じです。Pinky の匿名の利用統計には、画面、入力したテキスト、アプリ名が含まれることはありません（[上記](#利用統計クラッシュレポートフィードバック)）。

</details>

<details>
<summary><b>Pinky はスマートフォンに何をインストールしますか？</b></summary>

Android では、「インストール」をクリックしたときに Pinky Companion をインストールします。Pinky はそれを最新に保ち、そのユーザー補助サービスはエージェントが作業している間だけオンにします（[理由](#よくある質問)）。iPhone では、iOS 27 以降ではアプリを何もインストールしません：Xcode と同じように、Pinky は Apple の開発者ディスクイメージをマウントします。iOS 17.4〜26 では、Xcode から一度インストールする WebDriverAgent ヘルパーです（[方法](docs/notes/ios.md#installing-the-webdriveragent-helper)）。

</details>

<details>
<summary><b>銀行などのアプリは Pinky Companion に気づきますか？</b></summary>

ユーザー補助サービスがオンの間は、気づくことがあります。マルウェアはユーザー補助サービスを悪用するので、多くの銀行アプリは、アプリストア以外から入れたアプリがそれをオンにしている間はサインインを拒みます：マレーシア（Maybank、CIMB、HSBC など、マレーシア銀行協会の加盟行。2024 年から）やシンガポール（DBS、OCBC、UOB。2023 年から）などがそうです。Pinky Companion は Pinky が USB 経由でインストールするので、そのようなアプリにあたります。WeChat、Douyin、Xiaohongshu のようなアプリもこのサービスを見ることができ、自動化された利用を監視しています。これらが Pinky Companion を検出したという報告は聞いていませんが、エージェントにそれらを使わせることは、その利用規約に反する可能性があります。

アプリは、USB デバッグやワイヤレスデバッグがオンになっていることも見ることができます。これはすべての Android スマートフォンで Pinky に必要なもので、銀行アプリなど一部のアプリは、それがオンの間は警告したり起動を拒んだりします。Pinky でスマートフォンを使っている間は、ずっとオンのままです。

そのため、Pinky はエージェントがスマートフォンで作業している間だけサービスをオンにします：タスクが始まるときにオンにし、タスクが終わるか、あなたがスマートフォンを引き継いでから数秒後、あるいはエージェントの最後の手順から 1 分後にまたオフにします。それ以外のときはオフなので、それらのアプリはいつもどおり動きます。エージェントが作業している間は、スマートフォンのバックグラウンドのディスプレイであっても、それらのアプリはサービスを見ることができます：そのときにそれらのアプリを使うには、先に Pinky で **操作を引き継ぐ** をクリックするか、タスクを終わらせてください。

Pinky がサービスをまったくオンにしないようにするには、スマートフォンで Pinky Companion をアンインストールしてください（Android の設定 › アプリ）。Pinky はそれがなくても動きますが、遅くなります：画面の読み取りに 70 ms ではなく約 2 秒かかり、英語の普通の文字以外のテキストはスマートフォンのクリップボードを通して入力します。スマートフォンのページの **あとで** で、インストールの提案をもう一度出さないようにできます。

</details>

<details>
<summary><b>エージェント向けの他のスマートフォンのツールと Pinky は何が違いますか？</b></summary>

作る前に、10 以上のツールを調べました。オープンソースのものの中に、iPhone と Android の両方に対応し、macOS、Windows、Linux で動き、どんなエージェントとも使えて、しかも人が関与し続けられる、というすべてを一度に満たすものはありませんでした。ほとんどは、先に確認するかどうかをエージェントに任せるか、操作の種類ごとまとめてブロックします。Pinky は、危険なタップ 1 つを、エージェントの外側で自分が保留し、すべての実行を記録します。

</details>

<details>
<summary><b>コマンドを実行できるエージェントにスマートフォンを任せても安全ですか？</b></summary>

あなたとしてコマンドを実行できるエージェントは、たとえば adb を通して、Pinky を介さずにスマートフォンに届き、そのルールを飛ばせてしまいます。そのようなエージェントは、スマートフォンにアクセスできないサンドボックスの中で動かすか、スマートフォンを別のコンピューターにつないで、エージェントにはそこの Pinky に HTTP で接続させてください（[方法](docs/mcp.md#over-http)）。

</details>

## ソースからビルドする

Pinky を使うには、リリースをインストールしてください（[はじめに](#はじめに)）。開発のために自分でビルドするには、Go 1.27.3 以降と、Mac では Xcode のコマンドラインツールが必要です。

Pinky は cgo を使わない 1 つの Go のバイナリで、MyGo（`go tool mygo build`）がシステムごとにパッケージングします。ライブのミラーと録画には FFmpeg 9 の共有ライブラリを使い、Pinky はそれを実行時に読み込みます：`PINKY_FFMPEG_DIR` から、アプリの中（macOS）から、実行ファイルの隣から、そしてシステムがライブラリを置く場所から読み込みます。どのリリースにも独自の最小限の LGPL ビルド（`scripts/ffmpeg.sh`、H.264 と HEVC のデコーダー）が入っています：Pinky.app の中と、Windows のインストーラーと zip、Linux の `.tar.gz` と `.deb` では実行ファイルの隣です（[docs/RELEASING.md](docs/RELEASING.md#ffmpeg)）。自分でビルドする場合もそれが必要です：`make app` は Pinky.app にそれを追加し、`scripts/release.sh build` はそれを含めてすべてのパッケージを作ります。FFmpeg がなくてもミラー以外はすべて動き、Pinky は何が足りないかを伝えます。

### macOS

```sh
make companion   # optional: the Android companion APK (Android SDK and JDK)
make app         # build/pinky/darwin-<arch>/Pinky.app and pinky-<version>-<arch>.dmg
```

`make app` は、初回に最小限の LGPL の FFmpeg 9 をソースからビルドし（`make ffmpeg`、約 1 分：VideoToolbox 付きの H.264 と HEVC のデコーダー）、それを `Pinky.app/Contents/Frameworks` に置きます。CMake がインストールされていれば（`brew install cmake`）、Pinky 自身の adb（`make adb`）もビルドして `Contents/Helpers` に置き、コンパニオンの APK がビルドされていればそれも追加し（`COMPANION_APK=path` で別のものを選べます）、アプリに署名して、完成したアプリから `.dmg` を作ります。`.dmg` から Pinky を「アプリケーション」にドラッグしてインストールします。`cmd/pinky/mygo.json` の `macos.signingIdentity`（または `make app MACOS_SIGN=…`）で Developer ID を指定しない限り、アプリはアドホック署名になります。アドホック署名のアプリは、それをビルドした Mac では動きますが、別の Mac の Gatekeeper は拒否します。`make app` は公証しません。`make release` は `RELEASE_SIGN` の Developer ID で署名し、`NOTARY_PROFILE`（`pinky-notary`）の notarytool のプロファイルで `.dmg` を公証します（`make notarize`：送信、待機、ステープル）。プロファイルは `xcrun notarytool store-credentials pinky-notary --apple-id <Apple ID> --team-id <team>` で一度保存しておきます。リリースそのものは、両方の種類の Mac、Windows、Linux 向けに、ワークフローがビルド、署名、公開します。[docs/RELEASING.md](docs/RELEASING.md) を参照してください。

ビルドを試すには、それを開き（`open build/pinky/darwin-arm64/Pinky.app`）、その「設定 › エージェント」の「接続」でエージェントを接続します。「接続」はこのビルドのパスを使います。Claude Code ならチェックアウトから次のようにもできます：

```sh
claude mcp add --scope user pinky -- "$PWD/build/pinky/darwin-arm64/Pinky.app/Contents/MacOS/Pinky" mcp
mkdir -p ~/.claude/skills/pinky && cp docs/SKILL.md ~/.claude/skills/pinky/SKILL.md
```

`go run ./cmd/pinky` はアプリの FFmpeg を見つけられません：Homebrew で FFmpeg 9 をインストールする（`brew install ffmpeg`）か、ビルドしたものを指定してください（`PINKY_FFMPEG_DIR=build/ffmpeg/lib go run ./cmd/pinky`）。

コンピューターからスマートフォンのロックを解除する機能は、PIN をログインキーチェーンに保存し、毎回 Touch ID か Mac のパスワードを求めます。

### Windows

```sh
go tool mygo build -platform windows/amd64 cmd/pinky   # build/pinky/windows-amd64/Pinky.exe (and an installer where NSIS is installed)
go build -o pinky.exe ./cmd/pinky                      # a console build, for pinky devices and other commands in a terminal
```

Pinky.exe はウィンドウのプログラムです：`Pinky.exe mcp` を起動するエージェントはパイプでそれと通信しますが、ターミナルにはその出力が何も表示されないので、ターミナルではコンソール版のビルドを使ってください。リリースのインストーラーと zip には、`Pinky.exe` の隣に FFmpeg（D3D11VA 付き）が入っています。自分でビルドする場合は、`scripts/ffmpeg.sh build/ffmpeg-windows windows-amd64` が `build/ffmpeg-windows/bin` に DLL を作ります（Docker のコンテナーの中で）。それを実行ファイルの隣に置くか、`PINKY_FFMPEG_DIR` にそのフォルダーを設定してください（[FFmpeg-Builds](https://github.com/FFmpeg-Builds/FFmpeg-Builds/releases) の `ffmpeg-n9.0-…-win64-lgpl-shared` のビルドでも動きます）。自分でビルドしたものには adb が入っていないので、Android スマートフォンには Android の platform tools の adb が必要です。`scripts/adb.sh build/adb-windows windows-amd64` で Pinky 自身の adb をビルドでき（Docker のコンテナーの中で）、その `adb.exe` と 2 つの DLL を実行ファイルの隣に置きます。Windows での iPhone は未テストです。PIN は資格情報マネージャーに保存され、ロックの解除のたびに Windows Hello で、Hello が設定されていない場合はアカウントのパスワードで確認します。

### Linux

```sh
go tool mygo build -platform linux/amd64 cmd/pinky   # build/pinky/linux-amd64/: pinky, a .deb, a .tar.gz with install.sh, a PKGBUILD
```

ウィンドウには GTK 3 と EGL が必要です（`libgtk-3-0`、`libegl1`。GPU がない場合は Mesa がソフトウェアで描画します）。リリースの `.tar.gz` と `.deb` には `pinky` の隣に FFmpeg が入っています。glibc 2.29 以降（Ubuntu 20.04、Debian 11 以降）が必要で、ソフトウェアでデコードします。自分でビルドする場合は、`scripts/ffmpeg.sh build/ffmpeg-linux linux-amd64` が `build/ffmpeg-linux/lib` にライブラリを作ります（Docker のコンテナーの中で）。それを実行ファイルの隣に置くか、`PINKY_FFMPEG_DIR` にそのフォルダーを設定してください。ハードウェアでデコードする（VA-API）には、ディストリビューションのものや FFmpeg-Builds の共有ビルド（`libva2` と `libva-drm2` が必要です）など、それに対応した FFmpeg 9 を `PINKY_FFMPEG_DIR` で指定します。Pinky はそこを最初に探します。Linux での iPhone は未テストです。PIN は Secret Service（GNOME Keyring、KWallet、KeePassXC）の中の Pinky 専用のキーリングに保存されます。最初に保存するときにそのキーリングのパスワードを決めるよう求められ、Pinky は使うたびにまたロックするので、ロックの解除のたびにそのパスワードを求められます。

## ビルドとテスト

```sh
make check          # build (CGO_ENABLED=0), vet, test
make app            # Pinky.app with FFmpeg and the companion, and a .dmg (macOS)
make ffmpeg         # FFmpeg 9 for Pinky.app, in build/ffmpeg
make adb            # Pinky's own adb for Pinky.app, in build/adb (needs CMake)
make companion      # the Android companion APK (Android SDK and JDK needed)
make e2e            # a binary with MyGo's automation socket, for UI checks
scripts/smoke.sh    # the fake gateway, pinky devices and a task through pinky mcp
make dist           # a dry run of a release, in dist/<version>/ (docs/RELEASING.md)
```

`go run ./cmd/pinky --fake` は、スクリプトで動くスマートフォン 2 台（承認が必要な支払い、ブロックされた銀行アプリ、パスワードの欄）でアプリを動かすので、スマートフォンは必要ありません。

本物のスマートフォンが必要なテストには `device` のビルドタグを使います。[docs/WORKING.md](docs/WORKING.md) を参照してください。`go run ./scripts/secretcheck` は、ロックの解除で確認が求められること（Touch ID、Windows Hello）を手で確かめます。`PINKY_FFMPEG_APP=build/pinky/darwin-arm64/Pinky.app go test -run TestPackagedFFmpeg ./internal/video/` は、アプリが自分の FFmpeg でデコードすることを確かめます。展開した Windows や Linux のパッケージのフォルダーを渡すと、そのパッケージを確かめます。

CI（[.github/workflows/ci.yml](.github/workflows/ci.yml)）は、Linux と Windows でビルド、vet、テストを行い、macOS でビルドと vet を行い、3 つすべてで `scripts/smoke.sh` を実行し、Linux と Windows 向けにパッケージングし、Linux では Xvfb の下でウィンドウを開いて自動化ソケットでキャプチャします。`main`、プルリクエスト、手動での実行、プラットフォームのセッションの `claude/platform-*` ブランチで動きます。

## コードの構成

```text
cmd/pinky/                    app, daemon and MCP shim in one binary
internal/screen/              the screen format agents read
internal/driver/              driver interfaces; android/ and ios/ implement them
internal/gateway/             sessions, leases, the action pipeline, the client API
internal/policy/, approval/   rules and approvals
internal/history/             task history (SQLite)
internal/mcp/                 the MCP server
internal/api/                 the local socket for agents and clients
internal/demo/                scripted fake phones
internal/update/              new versions: the feed, checks, the swap, What's new
internal/video/, ui/          the desktop client (MyGo native UI, FFmpeg decoding)
internal/telemetry/           usage statistics, crash reports and feedback
worker/                       the Cloudflare Worker that receives usage statistics and crash reports
android/companion/            the Android companion app (Kotlin)
scripts/                      device locks and an MCP call script for checks
docs/                         design, agent guide, MCP reference and notes
```

## ドキュメント

- [受け入れのチェックリスト](docs/ACCEPTANCE.md)
- [設計](docs/DESIGN.md)
- [エージェント用ガイド（SKILL.md）](docs/SKILL.md)と [MCP リファレンス](docs/mcp.md)
- [外出先のスマートフォン](docs/away-from-home.md)
- [Pinky の開発](docs/WORKING.md)
- [利用統計、クラッシュレポート、フィードバック](docs/TELEMETRY.md)
- ノート：[integration](docs/notes/integration.md)、[gateway](docs/notes/gateway.md)、[Android](docs/notes/android.md)、[companion](docs/notes/companion.md)、[iOS](docs/notes/ios.md)、[client](docs/notes/client.md)、[updates](docs/notes/updates.md)、[telemetry](docs/notes/telemetry.md)
- [変更履歴](CHANGELOG.md)

## コミュニティ

質問をしたり、エージェントがスマートフォンで何をしているかを共有したり、開発を追いかけたりするには [Discord](https://discord.gg/S27b7SHjng) へどうぞ。バグや機能のリクエストは GitHub の issue で歓迎します。Pinky の「ヘルプ › フィードバックを送信…」を使うと、診断情報を書き込んだ issue が開きます。

## ライセンス

Apache-2.0

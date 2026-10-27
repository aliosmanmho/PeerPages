# Kurulum

PeerPages iki parçadan oluşur ve **yalnızca ajanı kurman gerekir**:

- **Ajan** (`peerpages`) — senin makinende çalışan tek binary.
- **Web uygulaması** — GitHub Pages'te duran statik sayfa. Ziyaretçi hiçbir şey
  kurmaz, sadece URL'yi açar.

> Proje henüz sürümlenmedi ve hiç etiket atılmadı (bkz. `CHANGELOG.md`).
> Aşağıdaki indirme adresleri **ilk release'den sonra** çalışacak; şimdilik
> kaynaktan derleme yolunu kullan.

## 1. Binary indir (önerilen)

Release sayfasından platformuna uygun arşivi indir:
<https://github.com/aliosmanmho/PeerPages/releases>

| İşletim sistemi | Dosya |
|---|---|
| Windows | `peerpages_<surum>_windows_amd64.zip` (ARM için `windows_arm64`) |
| macOS (Apple Silicon) | `peerpages_<surum>_darwin_arm64.tar.gz` |
| macOS (Intel) | `peerpages_<surum>_darwin_amd64.tar.gz` |
| Linux | `peerpages_<surum>_linux_amd64.tar.gz` (ARM için `linux_arm64`) |

Arşivi aç, `peerpages` (Windows'ta `peerpages.exe`) dosyasını PATH'inde bir
dizine koy. İndirdiğin dosyanın bütünlüğünü `checksums.txt` ile doğrulayabilirsin:

```sh
sha256sum -c checksums.txt --ignore-missing
```

macOS'te ilk çalıştırmada Gatekeeper uyarı verebilir (binary imzalı değil):
**Sistem Ayarları → Gizlilik ve Güvenlik** altından "yine de aç" seçilir.

## 2. `go install` ile

Go 1.27 veya üstü gerekir:

```sh
go install github.com/aliosmanmho/PeerPages/agent@latest
```

Binary `$(go env GOPATH)/bin` altına kurulur; bu dizinin PATH'inde olduğundan
emin ol. Bu yolla kurulan binary de sürümünü bilir (`runtime/debug` üzerinden),
yani `peerpages version` doğru etiketi basar.

> **Not:** Modül yolu `…/PeerPages/agent` ve Faz 7'de **kesinleşti**:
> proje adı `peerpages` olarak kalıyor, yani bu yol değişmeyecek.

## 3. Scoop / Homebrew (henüz yayınlanmadı)

`scoop-bucket` ve `homebrew-tap` depoları hazır değil; goreleaser
yapılandırmasında taslak olarak duruyor ve yükleme kapalı
(`.goreleaser.yaml` → `skip_upload: true`). Yayınlandığında:

```sh
# Windows
scoop bucket add aliosmanmho https://github.com/aliosmanmho/scoop-bucket
scoop install peerpages

# macOS / Linux
brew install --cask aliosmanmho/tap/peerpages
```

## 4. Kaynaktan derle

```sh
git clone https://github.com/aliosmanmho/PeerPages.git
cd PeerPages/agent
go build -o peerpages .
```

Geliştirme ortamının tamamı (web dahil) için: [DEV.md](DEV.md).

## Kurulumu doğrula

```sh
peerpages version
peerpages doctor
```

`version` sürüm, commit, tarih ve platformu basar. `doctor` relay'lere
erişimi, STUN'u ve NAT tipini sınar — bir sorun varsa bağlanmayı denemeden
önce burada görürsün.

## İlk çalıştırma

```sh
peerpages serve --target http://localhost:3000
```

Ajan her hedef için bir ziyaretçi adresi basar. Ayrıntılı kullanım, çoklu
hedef ve paylaşılan sır için [DEV.md](DEV.md), elle deneme adımları için
[MANUAL-TEST.md](MANUAL-TEST.md).

## Windows'ta servis olarak çalıştırma

Ajan bir servis **değildir**: normal bir ön plan sürecidir ve terminali
kapatınca durur. Oturum açınca kendiliğinden çalışmasını istiyorsan iki yol
var. İkisi de sadece dokümantasyon: PeerPages bir servis birimi kurmaz.

> Ajanın kendisi relay düşmesinden ve uyku/uyanmadan toparlanır (Faz 4);
> aşağıdaki yollar sürecin **çökmesi** ya da makinenin yeniden başlaması
> durumu için.

### Görev Zamanlayıcı (ek araç gerekmez)

```powershell
$action  = New-ScheduledTaskAction -Execute "C:\araclar\peerpages.exe" `
           -Argument "serve --target http://localhost:3000"
$trigger = New-ScheduledTaskTrigger -AtLogOn
$settings = New-ScheduledTaskSettingsSet -RestartCount 3 -RestartInterval (New-TimeSpan -Minutes 1)
Register-ScheduledTask -TaskName "PeerPages" -Action $action -Trigger $trigger -Settings $settings
```

Günlükler görev geçmişinde durur; ajanın kendi çıktısını görmek için
`--log-file` yerine yönlendirme kullan (`cmd /c peerpages … >> log.txt 2>&1`).

### NSSM (gerçek Windows servisi)

```powershell
nssm install PeerPages "C:\araclar\peerpages.exe" "serve --target http://localhost:3000"
nssm set PeerPages AppStdout "C:\araclar\peerpages.log"
nssm set PeerPages AppStderr "C:\araclar\peerpages.log"
nssm start PeerPages
```

**Dikkat:** servis olarak çalışan ajan, servis hesabının `~/.peerpages/key`
dosyasını kullanır — yani **peer-id'si farklı olur**. Aynı adresi korumak
istiyorsan anahtarı `--key` ile açıkça ver.

### macOS / Linux

macOS'te `launchd` (`~/Library/LaunchAgents/*.plist`), Linux'ta kullanıcı
servisi (`systemctl --user`) aynı işi yapar. Her ikisinde de ajanı **kullanıcı
oturumunda** çalıştır: hedef (`localhost:3000`) genelde kullanıcıya ait bir
geliştirme sunucusudur.

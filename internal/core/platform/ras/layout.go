package ras

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"unicode/utf16"
)

// Layout das estruturas RAS em x64 com empacotamento de 4 bytes (pshpack4
// do ras.h). Montamos byte a byte em vez de usar structs Go porque o Go
// alinharia ULONG_PTR/HRASCONN em 8 e deslocaria os campos seguintes.
// Os testes de layout fixam cada deslocamento.

// Tamanhos de cadeia (em WCHARs, sem o terminador) do ras.h / lmcons.h.
const (
	maxEntryName      = 256 // RAS_MaxEntryName
	maxPhoneNumber    = 128 // RAS_MaxPhoneNumber
	maxCallbackNumber = 128 // RAS_MaxCallbackNumber
	maxUserName       = 256 // UNLEN
	maxPassword       = 256 // PWLEN
	maxDomain         = 15  // DNLEN
	maxDeviceType     = 16  // RAS_MaxDeviceType
	maxDeviceName     = 128 // RAS_MaxDeviceName
	maxPath           = 260 // MAX_PATH
)

// RASDIALPARAMSW.
const (
	dpOffSize           = 0
	dpOffEntryName      = 4
	dpOffPhoneNumber    = dpOffEntryName + (maxEntryName+1)*2           // 518
	dpOffCallbackNumber = dpOffPhoneNumber + (maxPhoneNumber+1)*2       // 776
	dpOffUserName       = dpOffCallbackNumber + (maxCallbackNumber+1)*2 // 1034
	dpOffPassword       = dpOffUserName + (maxUserName+1)*2             // 1548
	dpOffDomain         = dpOffPassword + (maxPassword+1)*2             // 2062
	dpOffSubEntry       = 2096                                          // 2094 alinhado a 4
	dpOffCallbackID     = dpOffSubEntry + 4                             // 2100 (ULONG_PTR, 8 bytes)
	dpOffIfIndex        = dpOffCallbackID + 8                           // 2108 (WINVER ≥ 0x601)
	dpOffEncPassword    = dpOffIfIndex + 4                              // 2112 (LPWSTR, SDKs novos)
)

// Tamanhos aceitos de RASDIALPARAMSW, do mais novo ao mais antigo. A API
// devolve ERROR_INVALID_SIZE (632) quando não reconhece; tentamos o próximo.
var DialParamsSizes = []uint32{dpOffEncPassword + 8, dpOffEncPassword, dpOffIfIndex}

// RASCONNSTATUSW.
const (
	csOffSize       = 0
	csOffState      = 4
	csOffError      = 8
	csOffDeviceType = 12
	csOffDeviceName = csOffDeviceType + (maxDeviceType+1)*2 // 46
	csOffPhone      = csOffDeviceName + (maxDeviceName+1)*2 // 304
	csOffLocalEP    = 564                                   // 562 alinhado a 4
	csOffRemoteEP   = csOffLocalEP + 20                     // RASTUNNELENDPOINT = 20 bytes
	csOffSubState   = csOffRemoteEP + 20                    // 604
)

// ConnStatusSizes: Win7+ (com endpoints e subestado) e o formato antigo.
var ConnStatusSizes = []uint32{csOffSubState + 4, csOffLocalEP}

// RASCONNW.
const (
	cnOffSize        = 0
	cnOffHandle      = 4 // HRASCONN, 8 bytes, alinhado a 4
	cnOffEntryName   = 12
	cnOffDeviceType  = cnOffEntryName + (maxEntryName+1)*2   // 526
	cnOffDeviceName  = cnOffDeviceType + (maxDeviceType+1)*2 // 560
	cnOffPhonebook   = cnOffDeviceName + (maxDeviceName+1)*2 // 818
	cnOffSubEntry    = 1340                                  // 1338 alinhado a 4
	cnOffGUIDEntry   = cnOffSubEntry + 4                     // 1344
	cnOffFlags       = cnOffGUIDEntry + 16                   // 1360
	cnOffLUID        = cnOffFlags + 4                        // 1364
	cnOffCorrelation = cnOffLUID + 8                         // 1372 (WINVER ≥ 0x601)
)

// ConnSizes: Win7+ e Vista.
var ConnSizes = []uint32{cnOffCorrelation + 16, cnOffCorrelation}

// RASCONNSTATE relevantes.
const (
	RASCS_PAUSED       = 0x1000
	RASCS_DONE         = 0x2000
	RASCS_Connected    = RASCS_DONE
	RASCS_Disconnected = RASCS_DONE + 1
)

func putUTF16(buf []byte, off, maxChars int, s string) error {
	u := utf16.Encode([]rune(s))
	if len(u) > maxChars {
		return fmt.Errorf("texto com %d unidades UTF-16 excede o limite de %d", len(u), maxChars)
	}
	field := buf[off : off+(maxChars+1)*2]
	clear(field)
	for i, c := range u {
		binary.LittleEndian.PutUint16(field[i*2:], c)
	}
	return nil
}

func getUTF16(buf []byte, off, maxChars int) string {
	u := make([]uint16, 0, maxChars)
	for i := 0; i <= maxChars && off+i*2+2 <= len(buf); i++ {
		c := binary.LittleEndian.Uint16(buf[off+i*2:])
		if c == 0 {
			break
		}
		u = append(u, c)
	}
	return string(utf16.Decode(u))
}

// DialParams é um RASDIALPARAMSW montado em bytes.
type DialParams []byte

// NewDialParams cria a estrutura zerada com dwSize = size.
func NewDialParams(size uint32) DialParams {
	p := make(DialParams, size)
	binary.LittleEndian.PutUint32(p[dpOffSize:], size)
	return p
}

func (p DialParams) Size() uint32               { return binary.LittleEndian.Uint32(p[dpOffSize:]) }
func (p DialParams) SetEntry(s string) error    { return putUTF16(p, dpOffEntryName, maxEntryName, s) }
func (p DialParams) Entry() string              { return getUTF16(p, dpOffEntryName, maxEntryName) }
func (p DialParams) SetUser(s string) error     { return putUTF16(p, dpOffUserName, maxUserName, s) }
func (p DialParams) User() string               { return getUTF16(p, dpOffUserName, maxUserName) }
func (p DialParams) SetPassword(s string) error { return putUTF16(p, dpOffPassword, maxPassword, s) }

// PasswordFingerprint é o hash do campo de senha como está (para a
// credencial salva no Windows, é o hash do marcador). Nunca vai para log.
func (p DialParams) PasswordFingerprint() string {
	sum := sha256.Sum256(p[dpOffPassword : dpOffPassword+(maxPassword+1)*2])
	return hex.EncodeToString(sum[:])
}

// Wipe zera o campo de senha.
func (p DialParams) Wipe() { clear(p[dpOffPassword : dpOffPassword+(maxPassword+1)*2]) }

// Clone copia o buffer.
func (p DialParams) Clone() DialParams { return append(DialParams(nil), p...) }

// ConnStatus é o RASCONNSTATUSW decodificado.
type ConnStatus struct {
	State      uint32
	Error      uint32
	DeviceType string
	DeviceName string
}

// NewConnStatusBuffer cria o buffer com dwSize preenchido.
func NewConnStatusBuffer(size uint32) []byte {
	b := make([]byte, size)
	binary.LittleEndian.PutUint32(b, size)
	return b
}

// DecodeConnStatus lê o buffer preenchido por RasGetConnectStatusW.
// Buffer menor que o cabeçalho (estado e erro) devolve ConnStatus zerado;
// cadeias que ultrapassam o buffer são lidas só até onde ele alcança.
func DecodeConnStatus(b []byte) ConnStatus {
	if len(b) < csOffDeviceType {
		return ConnStatus{}
	}
	return ConnStatus{
		State:      binary.LittleEndian.Uint32(b[csOffState:]),
		Error:      binary.LittleEndian.Uint32(b[csOffError:]),
		DeviceType: getUTF16(b, csOffDeviceType, maxDeviceType),
		DeviceName: getUTF16(b, csOffDeviceName, maxDeviceName),
	}
}

// RasConn é um RASCONNW decodificado.
type RasConn struct {
	Handle uintptr
	Entry  string
}

// NewConnArray cria um vetor de n RASCONNW com dwSize preenchido no primeiro
// (é o que RasEnumConnections confere).
func NewConnArray(size uint32, n int) []byte {
	b := make([]byte, int(size)*n)
	binary.LittleEndian.PutUint32(b, size)
	return b
}

// DecodeConns lê count elementos de tamanho size. O número de elementos é
// limitado ao que cabe em b; size menor que o prefixo do elemento ou count
// não positivo devolvem vazio. Nunca entra em panic.
func DecodeConns(b []byte, size uint32, count int) []RasConn {
	if size < cnOffEntryName || count <= 0 {
		return nil
	}
	count = min(count, len(b)/int(size))
	out := make([]RasConn, 0, count)
	for i := 0; i < count; i++ {
		e := b[i*int(size):]
		out = append(out, RasConn{
			Handle: uintptr(binary.LittleEndian.Uint64(e[cnOffHandle:])),
			Entry:  getUTF16(e, cnOffEntryName, maxEntryName),
		})
	}
	return out
}

// EncodeConnForTest monta um RASCONNW (usado por testes e pelo fake).
func EncodeConnForTest(b []byte, size uint32, i int, h uintptr, entry string) {
	e := b[i*int(size):]
	binary.LittleEndian.PutUint32(e, size)
	binary.LittleEndian.PutUint64(e[cnOffHandle:], uint64(h))
	_ = putUTF16(e, cnOffEntryName, maxEntryName, entry)
}

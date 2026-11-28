-- Dissector do protocolo RAPOSA para o Wireshark.
--
-- Faz o Wireshark mostrar os campos do nosso cabeçalho (tipo, Seq, Ack,
-- SACK, flag de retransmissão...) em vez de bytes crus.
--
-- Como usar (uma das opções):
--   wireshark -X lua_script:tools/wireshark/raposa.lua
--   tshark    -X lua_script:tools/wireshark/raposa.lua -r captura.pcapng
--   ou copie para ~/.local/lib/wireshark/plugins/ e reinicie o Wireshark.
--
-- Filtros úteis:
--   raposa                         só pacotes do RAPOSA
--   raposa.flags.retrans == 1      só retransmissões
--   raposa.type == 10              só ACKs
--   raposa.type == 13 || raposa.type == 14   PING/PONG (escolha do servidor)

local raposa = Proto("raposa", "RAPOSA (Reliable ARQ Protocol Over UDP)")

local HEADER_SIZE = 30

local types = {
  [1] = "HELLO", [2] = "CHALLENGE", [3] = "AUTH", [4] = "AUTH_OK",
  [5] = "LIST", [6] = "GET", [7] = "PUT", [8] = "OK",
  [9] = "DATA", [10] = "ACK", [11] = "FIN", [12] = "ERR",
  [13] = "PING", [14] = "PONG",
}

local error_codes = {
  [1] = "senha inválida", [2] = "não autenticado", [3] = "arquivo não encontrado",
  [4] = "nome inválido", [5] = "hash divergente", [6] = "sessão desconhecida",
  [7] = "pedido inválido", [8] = "erro interno",
}

local fields = raposa.fields
fields.type     = ProtoField.uint8("raposa.type", "Tipo", base.DEC, types)
fields.flags    = ProtoField.uint8("raposa.flags", "Flags", base.HEX)
fields.retrans  = ProtoField.bool("raposa.flags.retrans", "Retransmissão", 8, nil, 0x01)
fields.window   = ProtoField.uint16("raposa.window", "Janela anunciada", base.DEC)
fields.connid   = ProtoField.uint32("raposa.connid", "ConnID", base.HEX)
fields.seq      = ProtoField.uint32("raposa.seq", "Seq", base.DEC)
fields.ack      = ProtoField.uint32("raposa.ack", "Ack (cumulativo)", base.DEC)
fields.sack     = ProtoField.uint64("raposa.sack", "SACK (bitmap)", base.HEX)
fields.length   = ProtoField.uint16("raposa.length", "Tamanho do payload", base.DEC)
fields.checksum = ProtoField.uint32("raposa.checksum", "Checksum (CRC32)", base.HEX)
fields.payload  = ProtoField.bytes("raposa.payload", "Payload")
fields.errcode  = ProtoField.uint8("raposa.error.code", "Código de erro", base.DEC, error_codes)
fields.errmsg   = ProtoField.string("raposa.error.message", "Mensagem de erro")
fields.request  = ProtoField.string("raposa.request", "Pedido")
fields.sha256   = ProtoField.bytes("raposa.fin.sha256", "SHA-256 do conteúdo")

-- Lista os pacotes marcados no bitmap: bit i = pacote Ack+1+i.
local function sack_list(sack, ack)
  local received = {}
  for position = 0, 63 do
    if sack:band(UInt64(1):lshift(position)) ~= UInt64(0) then
      received[#received + 1] = tostring(ack + 1 + position)
    end
  end
  return table.concat(received, ",")
end

function raposa.dissector(buffer, pinfo, tree)
  if buffer:len() < HEADER_SIZE then
    return 0
  end
  pinfo.cols.protocol = "RAPOSA"

  local packet_type = buffer(0, 1):uint()
  local flags = buffer(1, 1):uint()
  local seq = buffer(8, 4):uint()
  local ack = buffer(12, 4):uint()
  local sack = buffer(16, 8):uint64()
  local length = buffer(24, 2):uint()

  local subtree = tree:add(raposa, buffer(), "RAPOSA, " .. (types[packet_type] or "?"))
  subtree:add(fields.type, buffer(0, 1))
  subtree:add(fields.flags, buffer(1, 1)):add(fields.retrans, buffer(1, 1))
  subtree:add(fields.window, buffer(2, 2))
  subtree:add(fields.connid, buffer(4, 4))
  subtree:add(fields.seq, buffer(8, 4))
  subtree:add(fields.ack, buffer(12, 4))
  local sack_item = subtree:add(fields.sack, buffer(16, 8))
  subtree:add(fields.length, buffer(24, 2))
  subtree:add(fields.checksum, buffer(26, 4))

  -- Texto da coluna "Info"
  local name = types[packet_type] or ("tipo " .. packet_type)
  local info
  if packet_type == 9 or packet_type == 11 then        -- DATA, FIN
    info = string.format("%s seq=%d len=%d", name, seq, length)
  elseif packet_type == 10 then                         -- ACK
    info = string.format("ACK ack=%d eco=%d win=%d", ack, seq, buffer(2, 2):uint())
    if sack ~= UInt64(0) then
      local list = sack_list(sack, ack)
      sack_item:append_text(" (recebidos: " .. list .. ")")
      info = info .. " sack=[" .. list .. "]"
    end
  else
    info = string.format("%s seq=%d", name, seq)
  end
  if bit.band(flags, 1) == 1 then
    info = info .. " [RETRANSMISSÃO]"
  end

  -- Payload com significado conhecido
  if length > 0 and buffer:len() >= HEADER_SIZE + length then
    local payload = buffer(HEADER_SIZE, length)
    if packet_type == 12 then                           -- ERR
      subtree:add(fields.errcode, payload(0, 1))
      if length > 1 then
        local message = payload(1):string(ENC_UTF_8)
        subtree:add(fields.errmsg, payload(1), message)
        info = info .. ": " .. message
      end
    elseif packet_type >= 5 and packet_type <= 7 then   -- LIST, GET, PUT
      local text = payload:string():gsub("\n", " | ")
      subtree:add(fields.request, payload, text)
      info = info .. " (" .. text .. ")"
    elseif packet_type == 11 then                       -- FIN
      subtree:add(fields.sha256, payload)
    else
      subtree:add(fields.payload, payload)
    end
  end

  pinfo.cols.info = info
  return buffer:len()
end

-- Portas usadas nos exemplos e no benchmark. Para outra porta:
-- botão direito no pacote > Decode As... > RAPOSA
local udp_ports = DissectorTable.get("udp.port")
for _, port in ipairs({ 9000, 9001, 9100 }) do
  udp_ports:add(port, raposa)
end

#!/usr/bin/env node
// Independent, read-only Kuwo catalogue adapter. No account, personal cookie or fallback source.
// The optional DES codec is the unchanged UNM 0.28.0 snapshot; see its COPYING files.
const assert = require('node:assert/strict')
const http = require('node:http')
const https = require('node:https')
const fs = require('node:fs')
const path = require('node:path')
const positiveID = /^[1-9][0-9]{0,18}$/
const maximumJSON = 2 * 1024 * 1024
const maximumCatalogue = 500

function candidateAudioURL(raw) {
 try {
  const u = new URL(raw)
  const host = u.hostname.toLowerCase()
  if (u.username || u.password || !['http:','https:'].includes(u.protocol) ||
      (u.port && u.port !== (u.protocol === 'http:' ? '80' : '443')) ||
      !(host === 'kuwo.cn' || host.endsWith('.kuwo.cn'))) return null
  u.protocol = 'https:'
  u.port = ''
  return u
 } catch { return null }
}

function canonicalTrack(song) {
 const id = String(song.MUSICRID || '').replace(/^MUSIC_/, '')
 const durationMs = Number(song.DURATION) * 1000
 const artist=String(song.ARTIST || ''),album=String(song.ALBUM || '')
 if (!positiveID.test(id) || typeof song.SONGNAME !== 'string' || !song.SONGNAME.trim() ||
     !Number.isFinite(durationMs) || durationMs <= 0 || durationMs > 86400000 ||
     Buffer.byteLength(song.SONGNAME)>1000 || Buffer.byteLength(artist)>2000 || Buffer.byteLength(album)>2000) return null
 let picture = ''
 try { const u = new URL(song.picurl || song.PICURL || ''); if(u.protocol === 'https:' && !u.username && !u.password) picture = u.href } catch {}
 return {id:'kuwo:' + id,source:'kuwo',externalID:id,title:song.SONGNAME,artist,album,picture,durationMs,
   version:song.SONGNAME.match(/[（(]([^）)]+)[）)]/)?.[1] || '未标明录音版本'}
}

function rightsOf(song) {
 return {free:song.fpay === '0' && song.payInfo?.feeType?.song === '0' && song.payInfo?.feeType?.vip === '0' && song.payInfo?.cannotOnlinePlay === '0',
   preview:song.payInfo?.listen_fragment === '1'}
}

// Only a bounded leading audio range is read. Xing/Info frame counts are preferred;
// plain MPEG layer III can be conservatively checked using CBR size/duration.
function probeMP3(bytes, sizeBytes) {
 let offset=0
 if (bytes.subarray(0,3).toString() === 'ID3' && bytes.length >= 10) {
  offset=10+((bytes[6]&127)*2097152+(bytes[7]&127)*16384+(bytes[8]&127)*128+(bytes[9]&127))
  if(bytes[5]&16) offset+=10
 }
 for(let at=offset;at+4<Math.min(bytes.length,offset+4096);at++) {
  const a=bytes[at],b=bytes[at+1],c=bytes[at+2],d=bytes[at+3]
  const version=(b>>3)&3,layer=(b>>1)&3,index=c>>4,rateIndex=(c>>2)&3
  if(a!==255 || (b&224)!==224 || version===1 || layer!==1 || !index || index===15 || rateIndex===3) continue
  const rate=[44100,48000,32000][rateIndex]/(version===3?1:version===2?2:4)
  const kbps=(version===3?[0,32,40,48,56,64,80,96,112,128,160,192,224,256,320]:[0,8,16,24,32,40,48,56,64,80,96,112,128,144,160])[index]
  const channels=(d>>6)===3?1:2
  const xing=at+4+((b&1)?0:2)+(version===3?(channels===1?17:32):(channels===1?9:17))
  let durationMs=(sizeBytes-offset)*8/kbps, durationKind='estimated-cbr'
  if(xing+12<=bytes.length && ['Xing','Info'].includes(bytes.subarray(xing,xing+4).toString()) && (bytes.readUInt32BE(xing+4)&1)) {
   durationMs=bytes.readUInt32BE(xing+8)*(version===3?1152:576)/rate*1000
   durationKind='mpeg-frame-count'
  }
  return {durationMs:Math.round(durationMs),durationKind,sizeBytes,bitrate:kbps*1000}
 }
 return null
}

function classifyAudio(track, probe) {
 if(!track || !probe || !Number.isFinite(probe.durationMs)) return 'unavailable'
 return Math.abs(track.durationMs-probe.durationMs)<=Math.max(5000,track.durationMs*0.03)?'available':'unavailable'
}

function failure(status, code) { return Object.assign(new Error(code), {status,code}) }

// Explicit rejectUnauthorized never inherits a shared container's TLS=0 setting.
function getHTTPS(raw, {limit=maximumJSON, range=false}={}) {
 return new Promise((resolve,reject)=>{
  const u=new URL(raw)
  if(u.protocol!=='https:' || u.username || u.password || (u.port && u.port!=='443')) return reject(failure(503,'unsafe_upstream_url'))
  const request=https.request(u,{method:'GET',rejectUnauthorized:true,signal:AbortSignal.timeout(8000),headers:{'user-agent':'BetaGo-Music-Anonymous/1.0',...(range?{Range:'bytes=0-8191'}:{})}},response=>{
   const chunks=[];let bytes=0,ended=false
   const finish=()=>{if(ended)return;ended=true;resolve({status:response.statusCode,headers:response.headers,body:Buffer.concat(chunks)})}
   response.on('data',chunk=>{
    if(ended)return
    const keep=Math.min(chunk.length,limit-bytes)
    if(keep>0)chunks.push(chunk.subarray(0,keep))
    bytes+=chunk.length
    if(range && bytes>=limit){finish();response.destroy()}
    else if(bytes>limit){ended=true;response.destroy();reject(failure(503,'upstream_response_too_large'))}
   })
   response.on('end',finish)
   response.on('error',()=>{if(!ended)reject(failure(503,'upstream_read_failed'))})
  })
  request.setTimeout(6500,()=>request.destroy(new Error('timeout')))
  request.on('error',()=>reject(failure(503,'upstream_unavailable')))
  request.end()
 })
}

function createKuwoCatalogue(options={}) {
 const catalogue=new Map()
 const cataloguePath=options.cataloguePath || ''
 const bundledDES=path.resolve(__dirname,'vendor/kwDES.cjs')
 const desModule=options.desModule || process.env.MUSIC_KUWO_DES_MODULE || (fs.existsSync(bundledDES)?bundledDES:path.resolve(__dirname,'../.runtime/kuwo-des-cache/kwDES.cjs'))
 let codec
 try {
  if(cataloguePath && fs.existsSync(cataloguePath)) {
   const stat=fs.statSync(cataloguePath)
   if(stat.size>maximumJSON)throw failure(503,'catalogue_too_large')
   const saved=JSON.parse(fs.readFileSync(cataloguePath,'utf8'))
   if(saved.version===1 && saved.source==='kuwo') for(const record of (saved.records || []).slice(-maximumCatalogue)) {
    const t=record.track
    if(t?.source==='kuwo' && positiveID.test(t.externalID) && t.id==='kuwo:'+t.externalID && typeof t.title==='string' && t.durationMs>0 && typeof record.query==='string') {
     catalogue.set(t.externalID,{track:{id:t.id,source:'kuwo',externalID:t.externalID,title:t.title,artist:String(t.artist||''),album:String(t.album||''),picture:'',durationMs:t.durationMs,version:typeof t.version==='string'?t.version:'未标明录音版本'},query:record.query.slice(0,200),rights:{free:false,preview:false}})
    }
   }
  }
 } catch { throw failure(503,'invalid_catalogue_snapshot') }
 function persist() {
  if(!cataloguePath)return
  const records=[...catalogue.values()].map(({track,query})=>({track,query}))
  fs.mkdirSync(path.dirname(cataloguePath),{recursive:true,mode:0o700})
  fs.writeFileSync(cataloguePath+'.tmp',JSON.stringify({version:1,source:'kuwo',records}),{mode:0o600})
  fs.renameSync(cataloguePath+'.tmp',cataloguePath)
 }
 async function search(q) {
  q=String(q||'').trim()
  if(!q || q.length>200)throw failure(400,'invalid_query')
  const u=new URL('https://search.kuwo.cn/r.s')
  for(const [k,v] of Object.entries({correct:'1',vipver:'1',stype:'comprehensive',encoding:'utf8',rformat:'json',mobi:'1',searchapi:'6',all:q}))u.searchParams.set(k,v)
  const response=await getHTTPS(u)
  if(response.status!==200)throw failure(503,'catalogue_unavailable')
  let data
  try {data=JSON.parse(response.body)} catch {throw failure(503,'invalid_catalogue_response')}
  const songs=data.content?.find(item=>item.musicpage)?.musicpage?.abslist
  if(!Array.isArray(songs))throw failure(503,'invalid_catalogue_response')
  const tracks=[];const ids=new Set()
  for(const song of songs.slice(0,50)) {
   const track=canonicalTrack(song)
   if(!track || ids.has(track.id))continue
   ids.add(track.id)
   catalogue.delete(track.externalID)
   catalogue.set(track.externalID,{track,query:q,rights:rightsOf(song)})
   tracks.push(track)
  }
  while(catalogue.size>maximumCatalogue)catalogue.delete(catalogue.keys().next().value)
  persist()
  return tracks
 }
 async function detail(id) {
  if(!positiveID.test(id))throw failure(404,'not_found')
  if(!catalogue.has(id)) await search(id)
  const record=catalogue.get(id)
  if(!record)throw failure(404,'not_in_verified_catalogue')
  return record.track
 }
 async function resolve(id) {
  const track=await detail(id)
  const denied=reason=>({trackID:track.id,status:'unavailable',reason})
  const record=catalogue.get(id)
  // Refresh public rights/metadata and require the exact selected source recording.
  const fresh=await search(record.query)
  if(!fresh.some(t=>t.externalID===id))return denied('recording_not_in_current_catalogue')
  const current=catalogue.get(id)
  if(!current.rights.free)return denied(current.rights.preview?'provider_preview_requires_official_player':'provider_restricted_or_unknown_rights')
  if(!codec) {try{codec=require(desModule)}catch{throw failure(503,'kuwo_codec_not_configured')}}
  if(typeof codec.encryptQuery!=='function')throw failure(503,'kuwo_codec_not_configured')
  const query='corp=kuwo&source=kwplayer_ar_5.1.0.0_B_jiakong_vh.apk&p2p=1&type=convert_url2&sig=0&format=mp3&rid='+id
  const u=new URL('https://mobi.kuwo.cn/mobi.s')
  u.searchParams.set('f','kuwo');u.searchParams.set('q',codec.encryptQuery(query))
  const response=await getHTTPS(u,{limit:65536})
  if(response.status!==200)return denied('provider_resolve_unavailable')
  const raw=response.body.toString().match(/https?:[^\s$"<>]+/)?.[0]
  const audioURL=candidateAudioURL(raw)
  if(!audioURL)return denied('provider_audio_url_unavailable')
  // HTTP candidate is usable only after the exact returned resource passes TLS and Range.
  const audio=await getHTTPS(audioURL,{range:true,limit:8192})
  const size=Number(String(audio.headers['content-range']||'').split('/')[1])
  const contentType=String(audio.headers['content-type']||'').split(';')[0]
  if(audio.status!==206 || audio.body.length!==8192 || !Number.isSafeInteger(size) || size<8192 || contentType!=='audio/mpeg')return denied('audio_probe_failed')
  const probe=probeMP3(audio.body,size)
  if(classifyAudio(current.track,probe)!=='available')return denied('audio_duration_mismatch_or_unknown')
  return {trackID:track.id,status:'available',url:audioURL.href,
   expiresAt:new Date(Date.now()+120000).toISOString(),
   reason:'anonymous_free_recording_tls_and_duration_verified',
   expiryKind:'local_recheck_deadline',audio:{tlsVerified:true,contentType,...probe}}
 }
 return {search,detail,resolve,lyrics:async id=>({trackID:(await detail(id)).id,lrc:'',translatedLrc:''})}
}

function createAlternativeServer(options={}) {
 const catalogue=createKuwoCatalogue(options)
 let active=0
 const server=http.createServer(async(request,response)=>{
  response.setHeader('Content-Type','application/json; charset=utf-8')
  response.setHeader('Cache-Control','no-store')
  response.setHeader('X-Content-Type-Options','nosniff')
  const send=(status,data)=>{response.writeHead(status);response.end(JSON.stringify(data))}
  let acquired=false
  try {
   if(request.method!=='GET')return send(405,{error:'read_only_source'})
   const url=new URL(request.url,'http://127.0.0.1')
   if(url.pathname==='/health')return send(200,{ok:true,source:'kuwo',account:false})
   if([...url.searchParams.keys()].some(key=>key!=='q'))return send(400,{error:'unsupported_parameter'})
   if(active>=4)throw failure(503,'source_busy')
   active++;acquired=true
   if(url.pathname==='/search')return send(200,{tracks:await catalogue.search(url.searchParams.get('q'))})
   const route=url.pathname.match(/^\/(tracks|resolve|lyrics)\/([1-9][0-9]{0,18})$/)
   if(!route)return send(404,{error:'not_found'})
   const [,action,id]=route
   if(action==='tracks')return send(200,{track:await catalogue.detail(id)})
   if(action==='resolve')return send(200,await catalogue.resolve(id))
   return send(200,await catalogue.lyrics(id))
  } catch(error) {return send(error.status || 503,{error:error.code || 'source_unavailable'})}
  finally {if(acquired)active--}
 })
 server.headersTimeout=5000;server.requestTimeout=20000
 return server
}

function selftest() {
 assert.equal(candidateAudioURL('http://kw-lv.kuwo.cn/one.mp3')?.href,'https://kw-lv.kuwo.cn/one.mp3')
 for(const value of ['https://kuwo.cn.evil.test/x','https://user:pass@kw-lv.kuwo.cn/x','https://127.0.0.1/x','http://kw-lv.kuwo.cn:8080/x','data:audio/mp3,x'])assert.equal(candidateAudioURL(value),null)
 const raw={MUSICRID:'MUSIC_629335603',SONGNAME:'小星星 (伴奏版)',ARTIST:'贝乐虎儿歌',ALBUM:'贝乐虎儿歌大全',DURATION:'75'}
 assert.equal(canonicalTrack(raw)?.id,'kuwo:629335603')
 assert.equal(canonicalTrack(raw)?.version,'伴奏版')
 assert.equal(canonicalTrack({...raw,MUSICRID:'MUSIC_0'}),null)
 const bytes=Buffer.alloc(8192);Buffer.from('fffb90c4','hex').copy(bytes)
 const probe=probeMP3(bytes,1200000)
 assert.equal(probe?.durationMs,75000)
 assert.equal(classifyAudio(canonicalTrack(raw),{durationMs:11285}),'unavailable')
 assert.equal(classifyAudio(canonicalTrack(raw),probe),'available')
 console.log('alternative-source selftest: PASS')
}

module.exports={createAlternativeServer,createKuwoCatalogue,candidateAudioURL,canonicalTrack,probeMP3,classifyAudio}
if(require.main===module) {
 if(process.argv.includes('--selftest'))selftest()
 else {
  const port=Number(process.env.MUSIC_KUWO_PORT || 37836)
  if(!Number.isInteger(port) || port<1024 || port>65535)throw new Error('invalid_kuwo_port')
  const server=createAlternativeServer({cataloguePath:process.env.MUSIC_KUWO_CATALOGUE_PATH || ''})
  server.listen(port,'127.0.0.1',()=>console.log(JSON.stringify({service:'kuwo-read-only',address:'127.0.0.1',port})))
 }
}

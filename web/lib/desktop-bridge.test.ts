import {afterEach,describe,expect,it,vi} from 'vitest'
import {desktopBridge} from './desktop-bridge'

describe('Wails desktop bridge',()=>{
  afterEach(()=>vi.unstubAllGlobals())

  it('uses only the explicitly bound DesktopApp methods',async()=>{
    const saveProviderKey=vi.fn().mockResolvedValue(undefined)
    vi.stubGlobal('window',{go:{main:{DesktopApp:{SaveProviderKey:saveProviderKey}}}})
    const bridge=desktopBridge()
    expect(bridge).toBeDefined()
    await bridge?.saveProviderKey('openai','key-value')
    expect(saveProviderKey).toHaveBeenCalledWith('openai','key-value')
  })

  it('is unavailable in ordinary browser mode',()=>{
    vi.stubGlobal('window',{})
    expect(desktopBridge()).toBeUndefined()
  })
})

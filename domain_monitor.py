import re
import requests
from bs4 import BeautifulSoup
import os

# ---------- 请求配置 ----------
BASE_URL = "https://www.juyu.com/ykj/get_list"

# 从环境变量读取 Cookie（由 GitHub Secrets 注入）
COOKIE = os.getenv('COOKIE')  # 必须设置

HEADERS = {
    'cookie': COOKIE,
    'origin': 'https://www.juyu.com',
    'referer': 'https://www.juyu.com/ykj/',
    'user-agent': 'Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/149.0.0.0 Safari/537.36',
    'x-requested-with': 'XMLHttpRequest',
}

DATA = {
    'dqsj_1': '3000',
    'psize': '50',
    'page': '1',
    'jgpx': '3',
}

# ---------- Server酱 配置（从环境变量读取） ----------
SENDKEY = os.getenv('SENDKEY')  # 你的 Server酱 SendKey

# ---------- 获取第一页数据 ----------
def fetch_first_page():
    if not COOKIE:
        raise Exception("环境变量 COOKIE 未设置，请在 GitHub Secrets 中配置")
    resp = requests.post(BASE_URL, headers=HEADERS, data=DATA, timeout=30)
    resp.raise_for_status()
    result = resp.json()
    if result.get('code') != 1:
        raise Exception(f"请求失败: {result.get('msg')}")
    return result['html']

# ---------- 解析 HTML ----------
def parse_domains(html):
    soup = BeautifulSoup(html, 'html.parser')
    rows = soup.select('tbody tr')
    domains = []
    for row in rows:
        name_tag = row.find('a', class_='a_ym')
        if not name_tag:
            continue
        domain_name = name_tag.text.strip()
        tds = row.find_all('td')
        if len(tds) < 6:
            continue
        try:
            length = int(tds[1].text.strip())
        except:
            continue
        remain_text = tds[4].get_text()
        match = re.search(r'还剩(\d+)天', remain_text)
        if not match:
            continue
        remain_days = int(match.group(1))
        date_span = tds[4].find('span', class_='dqsj')
        expire_date = date_span.text.strip() if date_span else '未知'
        price_text = tds[5].get_text()
        price_match = re.search(r'(\d+)', price_text)
        if not price_match:
            continue
        price = int(price_match.group(1))
        domains.append({
            'name': domain_name,
            'length': length,
            'remain_days': remain_days,
            'expire_date': expire_date,
            'price': price
        })
    return domains

# ---------- 筛选、排序、取前5 ----------
def filter_top5(domains):
    filtered = [d for d in domains if d['length'] < 10 and d['remain_days'] > 3000]
    sorted_list = sorted(filtered, key=lambda x: x['price'])
    return sorted_list[:5]

# ---------- 通过 Server酱 推送 ----------
def send_notification(results):
    if not results:
        title = "【域名监控】今日无符合条件的域名"
        desp = "第一页中未找到到期>3000天且长度<10的域名。"
    else:
        title = "【域名监控】符合条件的 Top5 域名推荐"
        lines = ["域名\t长度\t到期时间\t价格(元)"]
        for d in results:
            lines.append(f"{d['name']}\t{d['length']}\t{d['expire_date']}\t{d['price']}")
        desp = "\n".join(lines)
    
    url = f"https://sctapi.ftqq.com/{SENDKEY}.send"
    params = {'title': title, 'desp': desp}
    resp = requests.get(url, params=params, timeout=30)
    resp.raise_for_status()
    result = resp.json()
    print(f"推送结果: {result}")

# ---------- 主流程 ----------
def main():
    print("正在获取第一页数据...")
    html = fetch_first_page()
    domains = parse_domains(html)
    print(f"解析到 {len(domains)} 条记录")
    top5 = filter_top5(domains)
    print(f"筛选后 Top5: {top5}")
    send_notification(top5)
    print("推送完成")

if __name__ == "__main__":
    main()
